# Events

An app on hearth publishes an event for each change that a consumer may want to
know about: a note is created, a bookmark moves. The events come from the same
Protobuf service as the app's API, so an event's type, its topic and the
permission a consumer needs to read it cannot drift from the app. The exact
contract is
[specs/events.md](https://github.com/frostyard/hearth/blob/main/docs/specs/events.md).

## Declare an event

An event is a message with the `hearth_event` option:

```proto
message NoteCreated {
  option (frostyard.hearth.v1.hearth_event) = {
    object: "note"
    verb: "created"
    selector: "folder"
    read_permission: "notelite:read"
  };
}
```

Its type is `org.frostyard.notelite.note.created`, and its topic in the
manifest is `notelite.note.created.folder`. When an RPC with the permission
`notelite:read` reads a `folder` selector, the event must carry that selector
too: a consumer's grant on a folder must be something the topic can show.

## What the plugin writes

`protoc-gen-hearth-events` writes 3 things for each event message:

```go
var EventNoteCreated = events.Spec{
	App:            "notelite",
	Type:           "org.frostyard.notelite.note.created",
	Topic:          "notelite.note.created.folder",
	Selector:       "folder",
	ReadPermission: "notelite:read",
}

func NewNoteCreated(ctx context.Context, subject, path string) (events.Event, error)
func EmitNoteCreated(ctx context.Context, w events.Writer, subject, path string) error
```

`subject` is the object's id, and `path` is the object's folder. An event with
no selector takes no path.

## A thin envelope

An event is a CloudEvents 1.0 envelope with no data:

```json
{
  "specversion": "1.0",
  "id": "019a8b6e-6a00-7000-8000-000000000001",
  "source": "/notelite",
  "type": "org.frostyard.notelite.note.created",
  "subject": "n-1",
  "time": "2026-11-16T07:00:00Z",
  "author": "local",
  "selector": "/work/2026"
}
```

- `id` is the event's own id, and `subject` is the object's: 2 events of 1
  note have 2 ids.
- `author` is the caller of the request's context: the principal that lodge
  named, or `local` for the app's own user. A context with no caller writes no
  event.
- The object itself is not in the event. A consumer reads it through the app's
  API, where the app checks the consumer's grant again.

## The topic carries the path

The topic is the manifest's topic, then 1 token for each segment of the
object's path. Each byte of a segment outside `A-Z`, `a-z`, `0-9` and `-` is
percent-encoded:

| Path | Topic |
| --- | --- |
| `/` | `notelite.note.created.folder` |
| `/work` | `notelite.note.created.folder.work` |
| `/Work Notes/2026_Q1` | `notelite.note.created.folder.Work%20Notes.2026%5FQ1` |

So a read grant on a folder is a set of topic filters, 1 for each event that
the grant's capability reads. `events.ReadFilters` gives them from the app's
event specs: an item `notelite:read` on `/work` reads
`notelite.note.created.folder.work` and `notelite.note.created.folder.work.>`,
the same for `note.deleted`, and nothing of an event that another capability
reads. lodge uses the same function's rule to filter what a consumer
receives.

## Write the event with the change

Write the event in the transaction of the change it reports, with the outbox
of that transaction:

```go
err := s.inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO notes (id, folder, title) VALUES (?, ?, ?)`,
		id, folder, title); err != nil {
		return err
	}
	return notesv1.EmitNoteCreated(ctx, outbox.Tx(tx), id, folder)
})
```

The event commits with the note, or rolls back with it. Create the outbox's
table once, beside the app's own tables, with `outbox.Init(ctx, db)`.

## Relay the events

A relay reads the outbox in order, publishes each event, and deletes it only
after the transport has it:

```go
relay := outbox.NewRelay(db, transport, outbox.Options{})
go relay.Run(ctx)
// After each commit:
relay.Notify()
```

A stop at any step loses no event: the relay publishes the event again, with the
same `id`, and the transport stores it once. A consumer still de-duplicates by
`id`, because delivery is at least once.

## Standalone: the in-process bus

An app with no lodge publishes to a bus in its own process. Its user interface,
or any part of the app, subscribes with a topic filter:

```go
bus := inproc.New()
cancel, err := bus.Subscribe("notelite.>", func(e events.Event) {
	log.Printf("%s %s", e.Type, e.Subject)
})
relay := outbox.NewRelay(db, bus, outbox.Options{})
```

A handler runs on the relay's goroutine: hand long work to another goroutine.

## On lodge: the broker socket

On lodge's host, the app publishes to lodge's broker socket instead. lodge
admits the app by its user id, and lets it publish only its own topics:

```go
transport, err := broker.Dial(broker.Options{Socket: "/run/lodge/nats.sock"})
if err != nil {
	return err
}
defer transport.Close()
relay := outbox.NewRelay(db, transport, outbox.Options{})
```

Each event is a JetStream publish with `Nats-Msg-Id` set to its id, so a repeat
after a lost acknowledgement is stored once. The app subscribes to nothing on
lodge: a consumer, such as an agent, subscribes through lodge, and receives only
the topics that its grant's read filters match.

## A worked example

[notes-lite](https://github.com/frostyard/hearth/tree/main/examples/notes-lite)
is a small app built from its README alone: its options, its outbox, its relay
and its stdio tools, in 1 module of its own.
