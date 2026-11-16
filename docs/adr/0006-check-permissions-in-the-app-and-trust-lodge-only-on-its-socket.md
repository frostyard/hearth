# 0006 — Check permissions in the app, and trust lodge only on its socket

- **Status:** Accepted
- **Date:** 2026-11-15

## Context

A hearth app serves 3 callers: its own user interface, the agents that lodge
routes to it, and the app's own user. frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
gives lodge the job of issuing access: it authenticates the user, looks up what
that user may do in the app, and forwards the call.

The same facts hold today:

- An app's permission is not 1 boolean. A user may read the notes of 1 group
  and no other, so a permission is a capability on a path, and the app's data
  decides which path a call touches.
- The request alone cannot answer the question. An RPC that names a note by id
  needs the note's group, and the note is in the app's database.
- The app's data changes. A check outside the change's transaction can pass on
  a note the change then moves, so the check must run inside the app's
  transaction.
- The manifest already carries each RPC's capability and its selector fields
  ([ADR-0004](0004-generate-the-manifest-from-protobuf-options.md)), so the
  rule of a check is known at generation time.
- A header is only as good as the socket it arrives on. Anything that can open
  a TCP connection can write `Lodge-Principal`, and 1 such connection would
  hand out any grant it names.
- The options are fixed ([specs/options.md](../specs/options.md)): a change of
  the option file moves every app's manifest, so a new option is not a small
  change.

## Decision

The app checks the permission, and it trusts lodge's headers only on the socket
that lodge alone can open.

- **A grant is a list of items.** 1 item is a capability, and a path for each
  selector it names. `{"cap":"demo:read","sel":{"group":"/work"}}` reads the
  notes under `/work`. An absent selector is the root path, and the grant is the
  union of its items: a check passes when 1 item covers it. The local user holds
  every capability on the root path.
- **A path is 1 tree.** A path starts with `/`, `/` alone is the root, and a
  path has no empty, `.` or `..` segment and no trailing `/`. A grant on a path
  covers the path and every path below it, and no sibling that only starts with
  its letters: a grant on `/Chan` does not cover `/Chani`. Every path is
  normalized to Unicode NFC before it is compared. A check on 2 values, such as
  a move's source and destination, passes only when each value is covered.
- **The generated check reads the request.** `protoc-gen-hearth-authz` writes 1
  check per RPC: the selector fields of the request become the values of the
  check, and an RPC with no selector field asks for the capability alone. An RPC
  whose request carries an object id — a singular field named `id` or ending in
  `_id` that holds no message, so a string, a number, an enum or bytes, through
  singular message fields — is an RPC on an object id: its generated check asks
  for the capability, and a generated hook resolves the stored object. A number
  or bytes counts as an id, so an app that names its ids with 1 is not left
  without a hook.
- **The hook runs in the app's transaction.** The wrapper that the plugin writes
  runs each object-id RPC through the app's `authz.TxFunc`: the hook reads the
  stored object, checks its selector value, and the RPC then changes that object
  in the same transaction. A handler that implements no hook does not satisfy
  the generated interface, so an app that forgets 1 hook does not build.
- **A list is filtered with the same grant.** The app calls
  `grant.Allows(cap, selector, path)` for each row it lists, and an invalid path
  is never allowed.
- **A refusal names the capability and the value.** The error is
  `permission denied: needs <cap> on <selector>=<path>`, or
  `permission denied: needs <cap>`, and the MCP tool that returns it returns
  that text.
- **lodge's headers are trusted only on lodge's socket.** The socket's listener
  checks the peer's user id with `SO_PEERCRED` (`LOCAL_PEERCRED` on macOS),
  before it reads a byte, and closes every connection of another user. On that
  socket `Lodge-Principal` is a principal and `Lodge-Grant` is the grant as
  base64url without padding of its JSON; the handler refuses the request with
  403 when either is missing or malformed.
- **The app's private socket is the local user.** Its handler deletes every
  `Lodge-*` header and sets the local user's caller, so a forged header reaches
  no handler.

## Consequences

- The manifest, the generated checks and the generated tools cannot drift: they
  come from 1 model.
- The permission check of an object-id RPC is the app's code, so it can be
  wrong; the generated check narrows the capability, and the hook decides the
  object. An app that checks nothing in a hook still denies every object whose
  capability the grant does not hold.
- The app holds no token and verifies no signature: its trust is the socket's
  peer, which is what the kernel knows and a remote caller cannot forge.
- A platform without a way to read the peer's user id refuses every lodge
  connection, so the app has no lodge socket there. That is deliberate: an
  unreadable peer is not a trusted peer.
- An app must supply 1 transaction function, and its hooks must run inside it.
  An app that has no transaction pays nothing: its service has no object-id RPC,
  and its wrapper runs each RPC directly.
- A path is a tree, not a string: an app that wants `/Chan` and `/Chani` as
  separate groups gets 2 separate grants, and no grant on 1 covers the other.
- The grant is 1 more format to get right, and a bad grant fails closed: an
  unknown member, an invalid capability or an invalid path is refused, not
  ignored.

## Alternatives considered

- **A check of the request fields only, with no hook:** rejected. It cannot
  answer an RPC that names an object by id, and the object's selector value is
  precisely what the app's data holds.
- **A new option to mark an RPC on an object id:** rejected. The options are
  fixed, and every app's manifest would change; the inference rule reads what the
  request already declares.
- **A check before the transaction, with the object read outside it:** rejected.
  The object can move between the check and the change, so the check would pass
  on a note the change no longer touches.
- **A signed grant, verified by the app:** rejected. It needs a key per app and a
  clock, and it adds nothing once the socket's peer is checked: lodge writes the
  grant, and no other process can open the socket.
- **Trusting the headers on every socket, and asking lodge to strip them:**
  rejected. The app's own user reaches the private socket, and a proxy that
  forgets to strip a header hands out a grant.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/permissions.md](../specs/permissions.md),
  [specs/options.md](../specs/options.md),
  [plan 0001, phase 2](../plans/0001-roadmap.md#phase-2--the-permission-runtime-the-lodge-adapter-and-mcp-built-2026-11-15)
- Builds on: [ADR-0004](0004-generate-the-manifest-from-protobuf-options.md)
  (the model that holds each RPC's capability and selectors)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
  (lodge issues access and forwards the call)
