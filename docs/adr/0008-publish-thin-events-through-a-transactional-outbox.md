# 0008 — Publish thin events through a transactional outbox

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

An app on hearth declares its events in its Protobuf service: each event
message carries `hearth_event` with an object, a verb, an optional selector and
a read permission, and the manifest lists each event's type, topic and read
permission (ADR-0004). lodge brokers those events to the consumers that may
read them (frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
These facts hold:

- **A consumer gets only what it may read.** lodge's broker filters by topic,
  and it reads no message: it gives each consumer a filter from the consumer's
  read grant (lodge
  [ADR-0022](https://github.com/frostyard/lodge/blob/main/docs/adr/0022-lodge-embeds-a-nats-broker-for-app-events.md)).
  So the topic must carry everything that a read grant scopes: the selector and
  the object's path (ADR-0004's rule that a read permission's selector is on its
  topic).
- **Content that sits in a broker is content that leaks.** The app already
  checks each read of its data (ADR-0006). An event that carries the object
  would need a second check in the broker, which reads no message.
- **A change and its event are 1 fact.** A change that commits without its
  event loses the event; an event published for a change that rolls back
  reports a change that never happened. A process can stop between any 2
  steps.
- **Delivery is at least once.** A broker's acknowledgement can be lost after
  the broker stored the message. lodge's JetStream stores 1 message for each
  `Nats-Msg-Id` within 2 minutes.
- **An app works with no lodge** (frostyard/core ADR-0062, ADR-0085). Its own
  user interface still needs its events.
- **The author is the caller.** lodge names the caller of each request on the
  app's lodge socket, and the private socket and stdio are the local user
  (ADR-0006). A request field can name anybody.
- **The app publishes on lodge's Unix socket.** lodge admits an app's
  connection on `/run/lodge/nats.sock` by its peer uid, lets it publish only
  `<id>.>`, and answers each publish with a JetStream `PubAck` (lodge ADR-0022).

## Decision

- **An event is a thin CloudEvents 1.0 envelope.** Structured JSON, with
  `specversion`, `id`, `source` (`/<app>`), `type`
  (`org.frostyard.<app>.<object>.<verb>`), `subject`, `time`, and 2 extensions:
  `author` and `selector`. It has no `data`. A consumer that wants the object
  reads it through the app's API, under its own grant.
  - `id` is the event's own UUIDv7; `subject` is the object's id. 2 events of 1
    object have 2 ids.
  - `author` is the principal of the caller in the context, or `local`. No
    caller is an error: an event with no author is never written.
  - `selector` is the object's canonical path (Unicode NFC, ADR-0006's path
    rules), and is absent for an event whose message names no selector.
- **The topic carries the path.** An event's topic is
  `<app>.<object>.<verb>.<selector>`, then 1 token for each segment of the
  object's path; the root path adds no token. A segment percent-encodes every
  byte outside `[A-Za-z0-9-]`, so no segment holds a `.`, a `*`, a `>` or a
  space. A topic longer than 1024 bytes, or an envelope longer than 32 KiB,
  is an error when the event is built and when the outbox writes it, so an
  event that no broker takes never commits.
  An event with no selector has the topic `<app>.<object>.<verb>`.
- **A read grant is a set of topic filters, 1 event type at a time.** A
  grant item gives filters only for the events whose read permission is the
  item's capability. On path G of the event's selector, the event's topic `T`
  gives `T.<G>` and `T.<G>.>`; on the root, `T` and `T.>`; an event with no
  selector gives `T` itself, and only to an item that holds no other selector
  below the root. Each filter is a subset of the app-wide filters
  `<app>.*.*.<selector>.<G>` and `<app>.>`, which would also match the events
  of another read permission. hearth computes them from the app's specs
  (`events.ReadFilters`), so lodge and the app encode a path 1 way.
- **The plugin writes 1 publisher per event.** `protoc-gen-hearth-events`
  writes, for each event message `M`, the event's `events.Spec` (`EventM`),
  `NewM` (which builds the envelope from the context's caller) and `EmitM`
  (which writes it to an `events.Writer`).
- **The event is written in the change's transaction.** `events/outbox` keeps
  a table, `hearth_outbox`, in the app's SQLite database; `outbox.Tx(tx)` is the
  writer of 1 transaction. The event commits with the change, or rolls back
  with it.
- **A relay publishes, then deletes.** `outbox.Relay` reads the table in
  insertion order, publishes each event, and deletes its row only after the
  transport acknowledged it. A failure stops the batch, so a later event never
  passes an earlier one. The relay runs at once, on `Notify` after a commit,
  and on an interval.
- **The id is the de-duplication key.** The NATS transport publishes each event
  with `Nats-Msg-Id` set to its id, so a publish that the relay repeats after a
  lost acknowledgement is stored once within JetStream's window. A consumer
  de-duplicates by `id` beyond that window.
- **2 transports, 1 outbox.** `events/inproc` is the standalone transport: the
  relay hands each event to the app's own subscribers, in order, and the bus
  skips an id it delivered among its last 1,024. `events/broker` publishes over
  a Unix socket to a NATS server with JetStream, such as lodge's, and waits for
  the `PubAck`. The app picks 1 at start; its handlers and its outbox do not
  change.
- **The NATS transport only publishes.** An app on lodge may subscribe to
  nothing but its own replies (lodge ADR-0022); a consumer subscribes through
  lodge, which owns the consumer and its filter.

## Consequences

- A consumer's grant and its topic filter are 1 rule, which lodge applies
  without reading a message.
- A move or a rename of a folder is several events, 1 per object and path: a
  grant follows paths, not folder ids.
- A consumer reads the object through the app's API to learn what changed, so
  it pays 1 call per event it cares about.
- An app that publishes events keeps its data in a SQL database that the outbox
  can share. The outbox speaks SQLite through `database/sql`.
- A change commits only when its event can be built: an object whose path makes
  a topic longer than 1024 bytes, or whose id makes an envelope longer than
  32 KiB, cannot be written. 1 row that no broker takes would stop the relay
  for every later event.
- A consumer's filters come from the app's event specs, which lodge reads from
  the manifest: a grant item never matches an event of another read
  permission.
- A consumer must de-duplicate by `id`: the relay can repeat an event after a
  crash, and the 2-minute window of JetStream does not cover every repeat.
- 1 relay serves 1 database. 2 relays on 1 database can publish 1 event twice,
  which the id de-duplicates within the window.
- hearth depends on `github.com/nats-io/nats.go`, and its tests on
  `github.com/nats-io/nats-server/v2` and `modernc.org/sqlite`. None is lodge's
  code.
- The `Connect` route of an app's API is still not built: the code generator
  it needs, `protoc-gen-connect-go`, publishes no release binary with a
  checksum for each platform, so `mise.lock` cannot pin it as hearth's
  invariants require. Its pin is a later decision of the
  [roadmap](../plans/0001-roadmap.md).

## Alternatives considered

- **The object in the event (fat events):** rejected. The broker would hold the
  app's data and could not check who reads it; the app's own check would be
  bypassed.
- **Publish after the commit, from the handler:** rejected. A crash between the
  commit and the publish loses the event, and a publish before the commit
  reports a change that can still roll back.
- **The object's folder id on the topic, not its path:** rejected. A grant is a
  path (ADR-0006), and the broker would need a table from ids to paths that
  changes with each move.
- **Topic segments in base64 or with a hash:** rejected. A consumer could no
  longer read a topic, and a prefix of a path would not be a prefix of its
  topic.
- **A subscription API in the NATS transport:** rejected. lodge's broker gives an
  app no subscription beyond its replies; consumers are lodge's clients.
- **An outbox behind an interface for every database:** deferred. The suite's
  apps keep SQLite, and 1 dialect is 1 tested table.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/events.md](../specs/events.md),
  [plan 0001, phase 3](../plans/0001-roadmap.md#phase-3--events-transports-and-the-example-app-built-2026-11-16)
- Builds on: [ADR-0004](0004-generate-the-manifest-from-protobuf-options.md)
  (each event's type, topic and read permission),
  [ADR-0006](0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md)
  (the caller in the context, and the path rules)
- External: [CloudEvents 1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md),
  [RFC 9562 (UUIDv7)](https://www.rfc-editor.org/rfc/rfc9562),
  [JetStream de-duplication](https://docs.nats.io/nats-concepts/jetstream/streams#duplicate-detection),
  [lodge ADR-0022](https://github.com/frostyard/lodge/blob/main/docs/adr/0022-lodge-embeds-a-nats-broker-for-app-events.md)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
