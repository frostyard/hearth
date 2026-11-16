# 0010 — Serve the Connect route with a go.mod-pinned generator

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

1 Protobuf service gives an app its API, its MCP tools, its permission checks,
its events and its manifest (frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
The API itself is still each app's own work: hearth writes the permission
checks, the MCP tools, the events and the manifest, and the app writes its HTTP
layer, its error mapping and its socket admission. These facts hold:

- **The API has 2 sockets, and 2 kinds of caller.** On its private socket the
  app's own user holds every capability. On its lodge socket, lodge forwards a
  user's call, after it authenticated the user, with the principal and the
  user's grant for this app (ADR-0006). A caller travels in the context, so 1
  route serves both sockets.
- **A refusal must reach the client as 1 code and 1 text.** An agent reads the
  code, and its operator reads the text: `permission denied: needs demo:read on
  group=/private` says what to grant. A plain HTTP status loses both.
- **The generated API is already the right shape.** `protoc-gen-hearth-authz`
  writes `<Svc>API` as `(context.Context, *Req) (*Res, error)` per RPC
  ([specs/permissions.md](../specs/permissions.md)), and
  `protoc-gen-hearth-mcp` writes `<Svc>AgentAPI` in the same shape
  ([specs/mcp-tools.md](../specs/mcp-tools.md)).
- **`protoc-gen-connect-go` publishes no release binary.** It has no artifact
  with a checksum for each platform, so it cannot be a tool of `mise.toml` and
  `mise.lock`, which is how this repository pins every tool. That fact deferred
  the route in [ADR-0008](0008-publish-thin-events-through-a-transactional-outbox.md).
- **A Go module pins a tool by its go.sum.** A `tool` directive of `go.mod`
  names the package, and `go.sum` holds the checksum of the module that builds
  it (frostyard/core ADR-0043, ADR-0023). `frostyard/frostmarks` pins
  `connectrpc.com/connect/cmd/protoc-gen-connect-go` this way today.
- **A `go run` plugin is no remote plugin.** buf runs a local command; the
  template's module supplies the command, and no Buf Schema Registry plugin
  runs (frostyard/core ADR-0085).

## Decision

- **hearth serves an app's API over Connect.** The new package
  `connectserver/` holds what an app writes once: `Private` and `Lodge` admit
  its 2 sockets, `Serve` runs the server, `UnixClient` dials a socket, and
  `HandlerOptions` and `Interceptor` map the permission runtime's errors.
  [specs/connect.md](../specs/connect.md) is the contract.
- **`protoc-gen-connect-go` is pinned by a `tool` directive of `go.mod`.** The
  repository requires `connectrpc.com/connect` and names
  `connectrpc.com/connect/cmd/protoc-gen-connect-go` as a tool, so `go.sum`
  carries the checksum and no artifact of a release page is needed. Each buf
  template runs it as `go run connectrpc.com/connect/cmd/protoc-gen-connect-go`,
  which is a local plugin. `internal/workflowcheck` fails `make verify` when a
  template runs a `go run` package that its module's `go.mod` does not pin.
- **The generator runs with `simple`.** The generated handler interface then
  takes `(ctx, *Req) (*Res, error)`, which is the API that
  `protoc-gen-hearth-authz` returns, so the app's API is the Connect handler and
  no adapter code sits between them. The generated client has the same shape, so
  it satisfies `<Svc>AgentAPI`, and 1 client serves the app's UI and its tools.
- **1 error mapping, in 1 place.** The interceptor turns a refusal of the
  permission runtime (`*authz.DeniedError`, `authz.ErrNoCaller`) into
  `CodePermissionDenied` with `err.Error()` as the message; an invalid path
  (`*authz.PathError`) into `CodeInvalidArgument`; `context.Canceled` and
  `context.DeadlineExceeded` into their codes; a `*connect.Error` stays as it
  is; and any other error is `CodeInternal`. A streaming call passes through.
- **The 2 sockets keep their own admission.** The private socket serves the API
  as `authz.LocalCaller()`, and it deletes every `Lodge-*` header, so a forged
  header reaches no handler. The lodge socket serves the API under
  `/connect/<service>/<method>`, where the `Lodge-Caller: principal` header and
  lodge's peer check admit the call. `/health` and every other route of the app
  sit outside the adapter.

## Consequences

- hearth depends on `connectrpc.com/connect` (v1.21.1), and its generated code
  depends on `connectrpc.com/connect` too. Neither is lodge's code.
- The invariant "No remote buf plugin runs" now reads: every plugin is a local
  binary that `mise.lock` pins, or a `go run` of a tool that a `go.mod` `tool`
  directive pins. `internal/workflowcheck` guards it.
- An app's module adds 1 `tool` line, and `go.sum` carries the generator's
  checksum. An app that installs a plugin binary some other way drifts from this
  rule, and the guard fails in this repository only.
- The Connect route is generated code, so `make gen-check` covers it: a change
  of a service renews its handler and its client like every other generated
  file.
- An app's own error reaches the client as `CodeInternal`, with its text. An app
  that needs another code returns a `*connect.Error` itself, and the interceptor
  leaves it alone.
- The interceptor maps unary calls, and the route has no streaming RPC today: a
  streaming RPC would carry its errors unmapped, and a later change decides
  whether to map them.
- The sockets carry HTTP/1.1 over a Unix socket: no TLS, no CORS and no browser
  session. lodge holds the browser's trust and forwards a call as a principal,
  so the app never reads a cookie.
- `connectserver.Serve` is the app's 1 server for a Unix socket, and an app that
  needs another transport (TCP in a test, say) uses the handler with its own
  `http.Server`. The peer check of `lodge.Listener` is what marks a connection
  as lodge's, and `ConnContext: lodge.ConnContext` is what the handler reads.

## Alternatives considered

- **A hearth plugin that writes the Connect handlers itself:** rejected. It
  duplicates `protoc-gen-connect-go`, and the app's module would carry the
  generator's protocol details instead of the upstream module's.
- **Waiting for a release binary with checksums in `mise.lock`:** rejected. No
  such artifact exists, and the route is the last unbuilt phase of the roadmap;
  a `tool` directive pins the generator by a checksum that is in the tree now,
  and core's ADR-0043 admits that pin for a Go tool.
- **Hand-wired handlers in each app:** rejected. Each app writes its own HTTP
  layer, its own error mapping and its own socket admission, so they drift: no
  app on hearth carries the permission runtime's text to a client today, and
  each app that adds a route decides its codes and its headers again.
- **A remote BSR plugin for Connect:** rejected. hearth runs no remote plugin,
  and the generator is a Go package of a module in `go.sum`.
- **The default handler signature, with `connect.Request` and
  `connect.Response`:** rejected. Every app would wrap and unwrap each call to
  reach its generated API, and the generated client would not satisfy
  `<Svc>AgentAPI`.
- **Serving the API as gRPC or plain REST:** rejected. Connect speaks gRPC, gRPC
  Web and its own protocol over 1 handler, with 1 client for Go and 1 for
  TypeScript, and it needs no proxy in front of a Unix socket.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [specs/connect.md](../specs/connect.md),
  [plan 0001, phase 4](../plans/0001-roadmap.md#phase-4--the-connect-route-built-2026-11-16)
- Builds on: [ADR-0006](0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md)
  (the caller in the context, and the trust of the lodge socket),
  [ADR-0007](0007-generate-mcp-tools-from-agent-rpcs.md) (the agent API that the
  Connect client satisfies),
  [ADR-0008](0008-publish-thin-events-through-a-transactional-outbox.md) (the
  consequence that deferred this route)
- External: [Connect](https://connectrpc.com/docs/introduction),
  [connectrpc/connect-go](https://github.com/connectrpc/connect-go),
  [frostyard/core ADR-0043](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md),
  [frostyard/core ADR-0023](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
