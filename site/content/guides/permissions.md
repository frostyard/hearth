# Permissions

An app on hearth checks its own permissions. lodge authenticates the user, looks
up what that user may do in the app, and forwards the call with 2 headers; the
app's generated check reads those and decides. This page shows what an app
writes, and what the plugins write for it. The exact contract is
[specs/permissions.md](https://github.com/frostyard/hearth/blob/main/docs/specs/permissions.md).

## What a grant holds

A grant is a list of items. 1 item is a capability, and a path for each selector
the item scopes:

```json
[
  {"cap": "demo:read", "sel": {"group": "/work"}},
  {"cap": "demo:write"}
]
```

The rules are short:

- A path starts with `/`, and `/` alone is the root. A path has no empty, `.` or
  `..` segment and no trailing `/`.
- A grant on a path covers the path and every path below it, and no sibling: a
  grant on `/Chan` covers `/Chani` never.
- An absent selector is the root, and the root covers everything.
- The grant is the union of its items: a check passes when 1 item covers it.
- A path is compared in Unicode NFC, so 2 spellings of `café` are 1 path.

The local user — the app's own user, on the app's private socket and its stdio
server — holds every capability on the root path.

## What the plugin writes

`protoc-gen-hearth-authz` writes 1 check for each RPC, from the RPC's permission
and its selector fields:

```go
// CheckNoteServiceListNotes checks the permission of
// frostyard.demo.v1.NoteService.ListNotes.
func CheckNoteServiceListNotes(grant authz.Grant, req *ListNotesRequest) error {
	return grant.Check("demo:read", authz.Sel{Selector: "group", Path: req.GetGroup()})
}
```

An RPC whose request carries an object id — a singular field named `id` or
ending in `_id` that holds no message, so a string, a number, an enum or bytes
— needs more than the request: the object's group is in the app's data. For that RPC the plugin writes a hook, and the wrapper runs the hook
inside the app's transaction:

```go
// AuthorizeGetNote checks the group the note is stored in.
func (s *NoteService) AuthorizeGetNote(ctx context.Context, tx *store, grant authz.Grant, req *demov1.GetNoteRequest) error {
	note, ok := tx.notes[req.GetId()]
	if !ok {
		return fmt.Errorf("no note %q", req.GetId())
	}
	return grant.Check("demo:read", authz.Sel{Selector: "group", Path: note.GetGroup()})
}
```

The hook is part of the interface the constructor takes, so an app that forgets
1 hook does not build:

```go
api := demov1.NewNoteServiceAuthz[*store](service, service.tx)
```

## A move checks both ends

An RPC that moves 1 object touches 2 paths. The destination is the request's
selector value, so the generated check reads it; the source is the stored value,
so the hook reads it:

```go
// The generated check: the destination.
if err := grant.Check("demo:write", authz.Sel{Selector: "group", Path: req.GetToGroup()}); err != nil {
	return err
}
// The hook: the source.
return grant.Check("demo:write", authz.Sel{Selector: "group", Path: note.GetGroup()})
```

A grant on the source alone refuses the move, and so does a grant on the
destination alone.

## A list is filtered

A check answers 1 call. A list that returns many rows filters each of them with
the same grant, and an invalid path is never in a list:

```go
if !grant.Allows("demo:read", "group", note.GetGroup()) {
	continue
}
```

## The refusal

A refused check names the capability and the value:

```
permission denied: needs demo:read on group=/private
```

An MCP tool returns that text as a tool error, so an agent reads it.

## lodge's headers

| Header | Value |
| --- | --- |
| `Lodge-Principal` | the caller's principal: `^[A-Za-z0-9@._-]{1,64}$` |
| `Lodge-Grant` | the grant's JSON, as base64url without padding |

The adapter reads them only on the app's lodge socket, and only after it has
checked the peer's user id with the kernel (`SO_PEERCRED` on Linux,
`LOCAL_PEERCRED` on macOS). A connection of another user is closed before a byte
is read. On the app's private socket the adapter deletes every `Lodge-*` header
and sets the local user, so a forged header reaches no handler.

```go
// The lodge socket: lodge only.
server := lodge.Serve(listener, lodgeUID, mux)
// The private socket: the local user, and no Lodge-* header survives.
private := &http.Server{Handler: lodge.PrivateHandler(mux)}
```

## Test an app's checks

The app's own tests cover its hooks, with the same helpers the runtime uses: a
grant from `authz.NewGrant`, a context from `authz.WithCaller`, and a store
behind the transaction.

```go
grant, err := authz.NewGrant(authz.Item{Cap: "demo:read", Sel: map[string]string{"group": "/work"}})
if err != nil {
	return err
}
ctx := authz.WithCaller(t.Context(), authz.Caller{Principal: "chani", Grant: grant})
if _, err := api.GetNote(ctx, &demov1.GetNoteRequest{Id: "n-1"}); err == nil {
	t.Fatal("a note in another group was readable")
}
```

The demo app of the repository's `internal/e2e` directory is a full example: its
tests prove the refusal, both ends of a move and the list filter.
