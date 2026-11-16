# Spec: The Connect route

This contract governs the HTTP API that 1 app on hearth serves from its
Protobuf service: the routes of its 2 Unix sockets, the headers that admit a
call, the error code and text a client reads, and the client that dials a
socket. Consumers: every app on hearth, lodge, which forwards a user's call, the
agents and the UI that call the app, and the tests of all of them. The runtime
is the Go package `github.com/frostyard/hearth/connectserver`, and the route is
the code that `protoc-gen-connect-go` generates.

## Interface

### The code of a service

For a service `S` of an app's proto file, `protoc-gen-connect-go` writes
`<app>/<file>v1connect/` with `simple` ([ADR-0010](../adr/0010-serve-the-connect-route-with-a-go-mod-pinned-generator.md)):

| Generated | Shape |
| --- | --- |
| `type SHandler interface` | 1 method for each RPC: `<Rpc>(context.Context, *Req) (*Res, error)` |
| `func NewSHandler(svc SHandler, opts ...connect.HandlerOption) (string, http.Handler)` | the path prefix `/<package>.S/`, and the handler |
| `type SClient` | `NewSClient(httpClient connect.HTTPClient, baseURL string, opts ...connect.ClientOption) SClient`, with the same method shapes |

`SHandler` is the `<Svc>API` that `protoc-gen-hearth-authz` returns
([specs/permissions.md](permissions.md)), and `SClient` satisfies `<Svc>AgentAPI`
([specs/mcp-tools.md](mcp-tools.md)), so 1 client serves an app's UI and its
tools.

### The routes

| Socket | Path | Admission |
| --- | --- | --- |
| the app's private socket | `/<package>.S/<Rpc>` | the connection's user is the app's own user, and the caller is `authz.LocalCaller()` |
| the app's lodge socket | `/connect/<package>.S/<Rpc>` | the connection is lodge's, the `Lodge-Caller` header is `principal`, and lodge's 2 headers carry the principal and the grant |
| either socket | the app's own routes, such as `/health` | the app's, outside the adapter |

A call is 1 `POST`. The lodge socket serves the route at
`connectserver.LodgePrefix` (`/connect/`), which is where lodge forwards a user's
call (lodge's `docs/specs/app-contract.md`). Both sockets delete no request body
and read the same handler: the difference is the caller in the context.

| Go | Meaning |
| --- | --- |
| `connectserver.LodgePrefix` | `/connect/`: the path under which lodge forwards a call |
| `connectserver.CallerHeader`, `connectserver.PrincipalCaller` | `Lodge-Caller`, and the only value that admits a call on the lodge socket: `principal` |
| `connectserver.Private(path string, h http.Handler) http.Handler` | `h` at `path` as the local user, and 404 elsewhere |
| `connectserver.Lodge(path string, h http.Handler) http.Handler` | `h` at `path`, mounted at `LodgePrefix`, for lodge's socket; 403 for another caller or another path |
| `connectserver.HandlerOptions(opts ...connect.HandlerOption) []connect.HandlerOption` | the error mapping, then `opts` |
| `connectserver.Interceptor() connect.Interceptor` | maps the error of each unary call with `Error`; a streaming call passes through |
| `connectserver.Serve(ctx context.Context, l net.Listener, h http.Handler) error` | 1 server on 1 socket, with `ConnContext: lodge.ConnContext` and a 10 s read of a header; nil after a clean stop |
| `connectserver.UnixClient(socket string) *http.Client` | 1 client of the server on 1 Unix socket |

### The error mapping

`connectserver.Error` returns a `*connect.Error`, and its message is the
original error's text, byte for byte.

| Error | Code |
| --- | --- |
| a `*connect.Error` | its own: it stays as it is |
| `*authz.DeniedError` | `permission_denied`, with `permission denied: needs <cap>` or `permission denied: needs <cap> on <selector>=<path>` |
| `authz.ErrNoCaller` | `permission_denied`, with `permission denied: no caller` |
| `*authz.PathError` | `invalid_argument` |
| `context.Canceled` | `canceled` |
| `context.DeadlineExceeded` | `deadline_exceeded` |
| any other error | `internal` |

The wire message is the error's text itself, and not a rendering of a wrapped
error: `connect.NewError(code, errors.New(text))`. 1 refused read of
`/private`, with a grant on `/work`, answers `403` and

```json
{"code":"permission_denied","message":"permission denied: needs demo:read on group=/private"}
```

### The client

`connectserver.UnixClient(socket)` dials the Unix socket, and it ignores the host
of the base URL, so a caller writes `http://localhost` for a private socket and
`http://localhost/connect` for a lodge socket. It sets the headers of a lodge
call itself, or through a `connect.Interceptor`:

```go
client := demov1connect.NewNoteServiceClient(connectserver.UnixClient(socket), "http://localhost/connect",
	connect.WithInterceptors(headers{ // Lodge-Caller: principal, Lodge-Principal, Lodge-Grant
		connectserver.CallerHeader: connectserver.PrincipalCaller,
		lodge.PrincipalHeader:     "chani",
		lodge.GrantHeader:         base64.RawURLEncoding.EncodeToString([]byte(`[{"cap":"demo:read","sel":{"group":"/work"}}]`)),
	}))
```

A generated client trims the trailing `/` of the base URL
(`strings.TrimRight(baseURL, "/")`), so `http://localhost/connect/` is the same
base as `http://localhost/connect`. A request URL that a caller builds by hand is
the caller's own: 2 slashes answer `307`, and a client that follows that answer
loses the call's body.

## Rules

- `connectserver.Private` sets the caller of every call to `authz.LocalCaller()`
  and deletes every `Lodge-*` header, so a forged `Lodge-Principal` or
  `Lodge-Grant` reaches no handler.
- `connectserver.Lodge` refuses with 403 and 1 line that starts `forbidden: `
  when the `Lodge-Caller` header is missing or is not `principal`, and when the
  path is not under `LodgePrefix`.
- `connectserver.Lodge` runs `lodge.Handler` inside its own checks, so a
  connection that `lodge.Listener` did not accept is refused with 403, though
  its headers are right.
- A socket admits 1 user id: the listener is `lodge.Listener(l, uid)`, and it
  closes every other connection before it reads a byte of it.
- The error mapping covers unary calls only, and the mapping is the same on both
  sockets.
- The route is HTTP/1.1 over a Unix socket: no TLS, no CORS, and no cookie.
- `/health` and every other route of an app sit outside the 2 adapters, so no
  caller bounds them.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| `internal/e2e/gen/frostyard/demo/v1/demov1connect/` | the demo app's route, from `testdata/e2e/buf.gen.yaml` |
| `examples/notes-lite/gen/example/notes/v1/notesv1connect/` | the example app's route, from its own `buf.gen.yaml` |

## References

- Rationale: [ADR-0010](../adr/0010-serve-the-connect-route-with-a-go-mod-pinned-generator.md),
  [ADR-0006](../adr/0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md)
- Context: [design/overview.md](../design/overview.md)
- Built in: [plan 0001, phase 4](../plans/0001-roadmap.md#phase-4--the-connect-route-built-2026-11-16)
