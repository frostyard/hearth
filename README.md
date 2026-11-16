<!-- readme-kind: tool -->
# hearth

hearth is a toolkit for frostyard's first-party apps: Protobuf options,
`protoc` plugins and a Go runtime, so that 1 Protobuf service gives an app its
API, its MCP tools, its permission checks, its events and a versioned manifest.
It is for the developers of those apps, and for the agents that call them.

**Status:** pre-release. The options' extension numbers are in-house until the Protobuf global extension registry grants one.

**Documentation:** [frostyard.github.io/hearth](https://frostyard.github.io/hearth/)

## What it does

- **1 service, every surface.** Annotate a Protobuf service with hearth's
  options, and the plugins generate the app's manifest
  ([the options](https://frostyard.github.io/hearth/guides/options/)).
- **A contract that cannot drift.** Every RPC declares its permission, its
  effect and where it is reachable; an RPC that declares none does not build.
- **A versioned manifest.** The plugin writes `manifest.json` in canonical
  JSON, and checks it against a JSON Schema
  ([the format](https://frostyard.github.io/hearth/reference/manifest/)).
- **A compatibility gate.** `hearth manifest diff OLD NEW` exits 1 when a
  breaking change needs the app's next major version
  ([the diff](https://frostyard.github.io/hearth/guides/manifest/)).

## Install

```sh
go install github.com/frostyard/hearth/cmd/hearth@latest
go install github.com/frostyard/hearth/cmd/protoc-gen-hearth-manifest@latest
```

[Install](https://frostyard.github.io/hearth/getting-started/install/) lists
the other ways, how to pin a version, and the requirements.

## Use

```sh
hearth manifest diff old/manifest.json manifest.json
hearth manifest validate manifest.json
```

The plugin runs from `buf`, which is how an app generates its manifest:

```yaml
version: v2
plugins:
  - local: protoc-gen-hearth-manifest
    out: gen
    opt: [app=marks, version=0.1.0]
```

## Develop

```sh
mise install   # the pinned tools: golangci-lint, buf, protoc-gen-go, svu
make ci        # what CI runs
```

[Build from source](https://frostyard.github.io/hearth/contributing/build/)
explains each target.

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs and plans.
- [SECURITY.md](SECURITY.md): report a vulnerability privately.
- [LICENSE](LICENSE): MIT.
