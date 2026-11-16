# hearth

Living document. Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md),
[ADR-0005](../adr/0005-run-the-repository-gate-in-go.md).
Contracts: [specs/options.md](../specs/options.md),
[specs/manifest.md](../specs/manifest.md).

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
    └──► protoc-gen-go ──► gen/go/.../options.pb.go
```

This document describes the parts that exist. The MCP, permission and event
plugins and the runtime are later tasks of the
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

### The generated code

`buf.gen.yaml` generates the options' Go code into `gen/go/` with the local
`protoc-gen-go` that `mise.lock` pins. `testdata/e2e/buf.gen.yaml` generates the
manifest of a small demo app into `testdata/e2e/gen/`. Both outputs are
committed, and `make gen-check` fails when a fresh run differs.

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
- A generated manifest that fails its own schema is an internal error of the
  plugin, not an app's problem: the plugin stops before it writes the file.

## References

- Rationale: [ADR-0004](../adr/0004-generate-the-manifest-from-protobuf-options.md),
  [ADR-0005](../adr/0005-run-the-repository-gate-in-go.md)
- Contracts: [specs/options.md](../specs/options.md),
  [specs/manifest.md](../specs/manifest.md)
- Built in: [plan 0001, phase 1](../plans/0001-roadmap.md#phase-1--options-model-manifest-and-the-gate-built-2026-11-15)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
