<!-- readme-kind: tool -->
# hearth

hearth is a toolkit for building apps whose API is also a good MCP server:
Protobuf options, `protoc` plugins and a Go runtime, so that 1 Protobuf service
gives an app its API, its MCP tools, its permission checks, its events and a
versioned manifest. It is for the developers of those apps, and for the agents
that call them.

**Status:** pre-release. The options' extension numbers are in-house until the Protobuf global extension registry grants one.

**Documentation:** [frostyard.github.io/hearth](https://frostyard.github.io/hearth/)

## What it does

- **3 options on your service.** `hearth_rpc` gives each RPC a permission, an
  effect (`read`, `write` or `outward`) and where it is reachable (UI, agent or
  both); `hearth_selector` marks the request field that scopes the permission,
  such as a folder; `hearth_event` declares an event
  ([the options](https://frostyard.github.io/hearth/guides/options/)).
- **4 plugins, 1 source.** `protoc-gen-hearth-manifest` writes the app's
  `manifest.json`, `protoc-gen-hearth-authz` its permission checks,
  `protoc-gen-hearth-mcp` its MCP tools, and `protoc-gen-hearth-events` its
  event publishers. An RPC with no permission or no effect does not build.
- **Permission checks in the app.** Each call is checked against the caller's
  grant, a list of capabilities on paths, and a stored object is checked in the
  app's own transaction
  ([permissions](https://frostyard.github.io/hearth/guides/permissions/)).
- **MCP tools from your RPCs.** Each agent RPC is a tool with a proto3 JSON
  schema and the hints of its effect, served over stdio with no token
  ([MCP tools](https://frostyard.github.io/hearth/guides/mcp/)).
- **Events that commit with the change.** An event is written to an outbox in
  the change's transaction, and a relay publishes it, losing none on a crash
  ([events](https://frostyard.github.io/hearth/guides/events/)).
- **A compatibility gate.** `hearth manifest diff OLD NEW` exits 1 when a
  change needs the app's next major version
  ([the manifest](https://frostyard.github.io/hearth/guides/manifest/)).

## Install

```sh
go install github.com/frostyard/hearth/cmd/hearth@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-mcp@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-events@latest
```

An app's module can also run each plugin with `go run` at the version its
`go.mod` requires.
[Install](https://frostyard.github.io/hearth/getting-started/install/) lists
the other ways, how to pin a version, and the requirements.

## Use

Build the example app, a small notes service, from its README alone:
[examples/notes-lite](examples/notes-lite/README.md). It walks through 1
service with the options, the `buf` template of the 4 plugins, the handlers
and the permission hooks, the outbox and the relay, and the stdio tools. Then
check a manifest:

```sh
hearth manifest validate gen/manifest.json
hearth manifest diff old/manifest.json gen/manifest.json
```

## How it works

An app on hearth runs in 1 of 2 modes, with the same handlers. **Standalone**,
it needs nothing else: its own user reaches it over its private socket or its
stdio MCP server as the local user, who holds every capability, and its events
go to an in-process bus. **On lodge**, frostyard's hub for first-party apps,
lodge authenticates each caller and passes the caller's grant on the app's
lodge socket, where hearth's adapter trusts it only after a peer check, and the
app publishes its events to lodge's broker socket, which delivers each event
only to a consumer whose grant reads its path. The
[design](docs/design/overview.md) shows how the parts fit.

## Develop

```sh
mise install   # the pinned tools: golangci-lint, buf, protoc-gen-go, svu
make ci        # what CI runs, the example app's gate included
```

[Build from source](https://frostyard.github.io/hearth/contributing/build/)
explains each target.

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs and plans.
- [SECURITY.md](SECURITY.md): report a vulnerability privately.
- [LICENSE](LICENSE): MIT.
