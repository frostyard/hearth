# The Connect route

1 Protobuf service gives an app on hearth its API as well as its MCP tools: the
same service, served over Connect, is what the app's own user interface and
lodge's callers reach. This page shows what an app writes for that route, and
what the generator writes for it. The exact contract is
[specs/connect.md](https://github.com/frostyard/hearth/blob/main/docs/specs/connect.md).

## The code the generator writes

`protoc-gen-connect-go` writes the route from the app's service, and it runs as
a tool of the app's module: its `go.mod` names it in a `tool` directive, so
`go.sum` carries its checksum and no release binary is needed.

```yaml
# buf.gen.yaml
plugins:
  - local:
      - go
      - run
      - connectrpc.com/connect/cmd/protoc-gen-connect-go
    out: gen
    opt:
      - paths=source_relative
      - simple
```

The `simple` option is what binds the 3 plugins together. The generated handler
takes `(ctx, *Request) (*Response, error)` per RPC, which is exactly the
`NoteServiceAPI` that `protoc-gen-hearth-authz` returns, so the app's API is the
Connect handler and no adapter sits between them:

```go
path, handler := notesv1connect.NewNoteServiceHandler(service.API(), connectserver.HandlerOptions()...)
```

The generated client has the same shape, so it satisfies the agent interface
that `protoc-gen-hearth-mcp` writes, and 1 client serves the app's UI and its
tools.

## The 2 sockets

```go
private := http.NewServeMux()
private.Handle(path, connectserver.Private(path, handler))
private.HandleFunc("/health", health)

onLodge := http.NewServeMux()
onLodge.Handle(connectserver.LodgePrefix, connectserver.Lodge(path, handler))
```

| Socket | Path | The caller |
| --- | --- | --- |
| the app's private socket | `/<package>.NoteService/<Rpc>` | the app's own user, who holds every capability |
| the app's lodge socket | `/connect/<package>.NoteService/<Rpc>` | the principal and the grant of lodge's 2 headers |

The private socket deletes every `Lodge-*` header of a request, so a forged
header reaches no handler. The lodge socket admits only lodge's own connection,
and only a call whose `Lodge-Caller` header is `principal`: lodge writes that
header after it authenticated the user. Your own routes, such as `/health`, sit
beside the adapter and outside it: no caller bounds them.

Each socket admits 1 user id, and the adapter closes every other connection
before it reads a byte of it. An app serves both through
`connectserver.Serve`, which takes the listener of `lodge.Listener`:

```go
connectserver.Serve(ctx, lodge.Listener(listener, os.Getuid()), private)
```

## What a refusal looks like

A refused call reaches the client as 1 Connect code and 1 text, so an agent
reads the code and its operator reads what to grant:

```json
{"code":"permission_denied","message":"permission denied: needs notelite:read on folder=/work"}
```

| The error | The code |
| --- | --- |
| `*authz.DeniedError` | `permission_denied` |
| `authz.ErrNoCaller` | `permission_denied` |
| `*authz.PathError` | `invalid_argument` |
| `context.Canceled` | `canceled` |
| `context.DeadlineExceeded` | `deadline_exceeded` |
| any other error | `internal` |

`connectserver.HandlerOptions()` adds that mapping to the app's handler, so no
handler writes it itself. An app that needs another code returns a
`*connect.Error` of its own, which the mapping leaves alone.

## Calling it

`connectserver.UnixClient(socket)` dials a Unix socket and ignores the host of
the base URL, so a test or a tool writes `http://localhost`. A lodge socket is
the same client under `/connect`, with the 3 headers of the caller.

```sh
curl --unix-socket notelite.sock -H 'Content-Type: application/json' \
  -d '{"folder": "/work"}' \
  http://localhost/example.notes.v1.NoteService/ListNotes
```

The example app of
[examples/notes-lite](https://github.com/frostyard/hearth/blob/main/examples/notes-lite/README.md)
serves both sockets, and its tests prove the code and the text of a refusal.

## What it does not do

- No TLS, no CORS and no cookie: the route is HTTP/1.1 over a Unix socket.
  lodge holds the browser's trust and forwards a call as a principal.
- No streaming mapping: the interceptor maps unary calls, and the route has no
  streaming RPC today.
- No second transport: an app that needs TCP uses the handler with its own
  `http.Server`, and keeps `ConnContext: lodge.ConnContext` for the lodge
  socket.

## References

- [ADR-0010](https://github.com/frostyard/hearth/blob/main/docs/adr/0010-serve-the-connect-route-with-a-go-mod-pinned-generator.md):
  why the route is generated, and why its generator is a tool of the module.
- [specs/connect.md](https://github.com/frostyard/hearth/blob/main/docs/specs/connect.md):
  the contract.
- [Permissions](permissions.md): the grant and the check behind each refusal.
