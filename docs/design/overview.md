# hearth

Living document. Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md),
[ADR-0005](../adr/0005-run-the-repository-gate-in-go.md),
[ADR-0006](../adr/0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md),
[ADR-0007](../adr/0007-generate-mcp-tools-from-agent-rpcs.md).
Contracts: [specs/options.md](../specs/options.md),
[specs/manifest.md](../specs/manifest.md),
[specs/permissions.md](../specs/permissions.md),
[specs/mcp-tools.md](../specs/mcp-tools.md).

## Overview

hearth is a toolkit for frostyard's first-party apps: Protobuf options,
`protoc` plugins and a Go runtime, so that 1 Protobuf service gives an app its
API, its MCP tools, its permission checks, its events and its versioned
manifest (frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
lodge is the hub of those apps: it issues access, routes MCP and brokers
events.

```
app.proto ──► buf ──► protoc-gen-hearth-manifest ──► manifest.json ──► lodge
    │                                                 │
    │                                                 └──► hearth manifest diff OLD NEW
    ├──► protoc-gen-go ──────────────► <app>/api.pb.go
    ├──► protoc-gen-hearth-authz ────► <app>/api_hearth_authz.go ──► authz/
    └──► protoc-gen-hearth-mcp ─────► <app>/api_hearth_mcp.go ─────► mcpserver/
                                                                        │
   lodge ──Lodge-Principal──────────► lodge/ ──authz.Caller────────────┤
   local user ──private socket──────► lodge/PrivateHandler ────────────┤
   local user ──stdio───────────────► mcpserver.ServeStdio ────────────┘
```

This document describes the parts that exist. The events plugin, the
transports and the example app are later tasks of the
[roadmap](../plans/0001-roadmap.md); this design doc grows with them.

## Design

### The options

`proto/frostyard/hearth/v1/options.proto` declares 3 options, and nothing else:

| Option | On | Meaning |
| --- | --- | --- |
| `hearth_rpc` (51370) | a method | the RPC's permission, effect and exposure |
| `hearth_selector` (51371) | a field | the selector name that the field carries |
| `hearth_event` (51372) | a message | the event's object, verb, selector and read permission |

The numbers 51370 to 51379 are hearth's in-house range until the Protobuf
global extension registry grants a number. [specs/options.md](../specs/options.md)
is the exact contract.

### The model

`internal/model` reads the files that a plugin is asked to generate, builds the
app model (operations, events, capabilities, selectors) and checks every rule.
It reports all of its problems at once, 1 line each, sorted, each prefixed with
the element's full name, and the plugin writes no file when any rule fails.
Every plugin of hearth reads this model, so the rules are checked once.

### The manifest

`cmd/protoc-gen-hearth-manifest` turns the model into `manifest.json` at the
output root, in canonical JSON, after validating it against
`schema/manifest-v1.json`. `manifest/` holds the format's Go types, the
canonical encoder, the reader and the compatibility rules.
[specs/manifest.md](../specs/manifest.md) is the exact contract.

### The CLI

`cmd/hearth` holds 2 commands:

- `hearth manifest diff OLD NEW` prints each breaking change of a new manifest
  against the old 1, then the verdict, and exits 1 when a breaking change needs
  the app's next major version.
- `hearth manifest validate FILE` checks 1 manifest against the schema.

### The permission runtime

`authz/` is the permission runtime: a grant is a list of items, each a capability
on a path, and a check answers whether a grant covers a capability on 1 or more
selector values. The path is a tree (`/Chan` does not cover `/Chani`), an absent
selector value is the root, and the grant is the union of its items. The caller
travels in the context, so every layer reads 1 caller and checks nothing itself.
A transaction value travels in the context too, so an app's hook and the RPC it
guards read and change 1 object in 1 transaction.
[specs/permissions.md](../specs/permissions.md) is the exact contract.

### The permission checks

`cmd/protoc-gen-hearth-authz` writes 1 check for each RPC of a service, and the
wrapper that runs it. An RPC whose request carries an object id also gets a hook:
the app reads the stored object inside its transaction, and checks the object's
selector value. The hook is part of the generated interface, so an app that
forgets 1 does not build.

### The lodge adapter

`lodge/` is the only place that trusts lodge. Its listener reads the peer's user
id with `SO_PEERCRED` before it reads a byte, and it closes every connection of
another user. On that socket the handler reads `Lodge-Principal` and
`Lodge-Grant` (base64url without padding of the grant's JSON), and refuses
anything else with 403. The app's private socket deletes every `Lodge-*` header
and sets the local user's caller.

### The MCP tools

`cmd/protoc-gen-hearth-mcp` writes 1 tool for each RPC whose exposure holds
`agent`: its name, its description, its effect as the protocol's hints, and the
JSON Schema of the proto3 JSON form of its request and response.
`mcpserver/` serves those tools with the Go MCP SDK: it puts the caller in the
call's context, returns the response as structured content, and turns a refusal
into a tool error with the permission runtime's own text. `mcpserver.ServeStdio`
serves the same tools on 2 streams, as the local user, with no token.
[specs/mcp-tools.md](../specs/mcp-tools.md) is the exact contract.

### The generated code

`buf.gen.yaml` generates the options' Go code into `gen/go/` with the local
`protoc-gen-go` that `mise.lock` pins. `testdata/e2e/buf.gen.yaml` generates the
Go code, the permission checks and the MCP tools of a small demo app into
`internal/e2e/gen/`, and its manifest into `testdata/e2e/gen/`. All of those
outputs are committed, `make gen-check` fails when a fresh run differs, and
`internal/e2e` is the demo app those outputs build.

## Operational notes

- `make ci` is the gate to run before a push. It needs Go and the tools of
  `mise.toml`: `mise install` provisions golangci-lint, buf, protoc-gen-go and
  svu.
- A change of the options changes the generated Go code and every golden
  manifest. Run `make generate` and commit the result; `make gen-check`
  regenerates into a temporary directory, shows each file that differs, and
  sweeps that directory away.
- `buf.lock` sits beside `buf.yaml` in the tree, and `buf dep update` keeps it.
  It pins no module today: `google/protobuf/descriptor.proto` is built into
  buf.
- No test uses the network, a real clock or a fixed path: the plugin's tests
  compile their Protobuf files in memory with `protocompile`.
- The lodge tests use a Unix socket under a short directory of
  `os.MkdirTemp("", "hearth")`, because a socket's path is 108 bytes at most.
- `internal/e2e` builds 1 fixture that must not compile, with the tag
  `hearth_missinghook`: it proves that an app without a permission hook does not
  build.
- A generated manifest that fails its own schema is an internal error of the
  plugin, not an app's problem: the plugin stops before it writes the file.

## References

- Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md),
  [ADR-0005](../adr/0005-run-the-repository-gate-in-go.md),
  [ADR-0006](../adr/0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md),
  [ADR-0007](../adr/0007-generate-mcp-tools-from-agent-rpcs.md)
- Contracts: [specs/options.md](../specs/options.md),
  [specs/manifest.md](../specs/manifest.md),
  [specs/permissions.md](../specs/permissions.md),
  [specs/mcp-tools.md](../specs/mcp-tools.md)
- Built in: [plan 0001, phase 1](../plans/0001-roadmap.md#phase-1--options-model-manifest-and-the-gate-built-2026-11-15),
  [plan 0001, phase 2](../plans/0001-roadmap.md#phase-2--the-permission-runtime-the-lodge-adapter-and-mcp-built-2026-11-15)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
