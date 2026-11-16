# 0004 — Generate an app's manifest from hearth's Protobuf options

- **Status:** Accepted
- **Date:** 2026-11-15

## Context

A first-party app on hearth writes 1 Protobuf service
(frostyard/core [ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
From those files hearth's plugins generate the app's MCP tools, permission
checks, events and its versioned manifest. lodge serves the app from that
manifest, and asks for consent again when a manifest's compatibility changes.

The same facts hold today:

- Protobuf has no standard place for an app's permission, its effect, where it
  is reachable and the selector that scopes it. Without an option, the rules
  hide in comments and in each app's own generator.
- 1 generator must serve every app, so the rules are in 1 place: this
  repository.
- The manifest is the app's contract. lodge reads it, and a breaking change to
  it must fail the app's own `make ci`, not a later deployment.
- hearth must be usable outside frostyard, so it holds no lodge code and no
  vendor-specific schema language.

## Decision

hearth defines 3 Protobuf options in `proto/frostyard/hearth/v1/options.proto`,
and generates an app's manifest, `manifest.json`, from them with the plugin
`protoc-gen-hearth-manifest`.

- **The options.** `MethodOptions.hearth_rpc` (extension 51370) carries the
  required `permission` (`<app>:<capability>`), the required `effect` (`read`,
  `write` for an undoable change, `outward` for an irreversible change or one
  that leaves the system), the non-empty `expose` list (`ui`, `agent`), an
  agent description and JSON examples of the request.
  `FieldOptions.hearth_selector` (51371) names the selector a request field
  carries. `MessageOptions.hearth_event` (51372) declares an event's object,
  verb, selector and read permission.
- **The in-house range.** The numbers 51370 to 51379 are hearth's until the
  Protobuf global extension registry grants a number. A number in that range
  is not used for anything else.
- **The parameters.** The plugin takes `app=<id>` and
  `version=<major.minor.patch>`, both required; any other parameter is an
  error.
- **The shared model.** `internal/model` builds the app model from the files a
  plugin is asked to generate and checks every rule: the permission's app
  prefix, the effect, the exposure, the tool name, the agent RPC's shape, the
  selectors, the event's object, verb, topic and read permission. 1 line for
  each problem, sorted, each prefixed with the element's full name, and no file
  is written when any rule fails. The MCP, permission and event plugins of
  later tasks read the same model.
- **The manifest.** Format major 1: the members `hearth`, `app`, `version`,
  `capabilities`, `selectors`, `operations` and `events`, in canonical JSON.
  `schema/manifest-v1.json` is the format's JSON Schema, and the plugin
  validates the manifest against it before it writes the file.
- **Compatibility.** `hearth manifest diff OLD NEW` runs in the app's
  `make ci` against the default branch. A removed operation, tool or event, or
  a changed permission, effect, selector or topic of an existing 1, is
  breaking. A selector is its request field and its name together: a selector
  that moves to another field is breaking, because the generated permission
  check reads the value from that field. A breaking change needs the app's next major version: for an app at
  `>= 1.0.0`, a greater major number; for an app at `0.y.z`, a greater minor
  number (svu's `v0: true` rule).
- **Tests.** The plugin's tests compile Protobuf files in memory with
  `github.com/bufbuild/protocompile`, build a `CodeGeneratorRequest`,
  marshal it and unmarshal it again so that the options parse as their
  generated types, and compare the output with committed golden manifests.
  The tests need no network and no `protoc` binary.
- **Generation is committed.** `make generate` runs the local plugins from
  `mise.lock`, and `make gen-check` regenerates into a temporary directory and
  fails when a file of `gen/` or of `testdata/` differs from a fresh run. No
  remote BSR plugin runs, and `buf.lock` pins each buf module dependency by
  digest.

## Consequences

- 1 Protobuf service gives an app its API, its MCP tools, its permission checks,
  its events and its manifest, and they cannot drift from each other.
- An RPC without a declared permission, effect or exposure does not build.
- An agent RPC cannot carry `google.protobuf.Any` or a map of messages: its
  tool schema would not be a faithful description of the call.
- A read permission that a selector scopes must appear on the topic of every
  event that names it, so a consumer's grant can filter events before delivery.
- The manifest is 1 more generated file per app, and a change of the options or
  of the plugin is a change of every app's manifest; `make gen-check` shows it
  in the pull request.
- The extension numbers are provisional. A granted number moves them in 1
  change across the option file, the generated code and every app's manifest.
- The format's major version, not the app's, decides who may read a manifest:
  lodge disables an app whose manifest it does not read, with a reason that
  names both versions.

## Alternatives considered

- **A YAML or JSON schema per app, with its own generator:** rejected. It is
  frostyard's own schema language, with its own docs, editor support and
  compatibility checking, and it drifts from the app's API.
- **Rules in doc comments, read by a `go generate` tool:** rejected. Comments
  are not checked by the compiler, and the rules would hide from review.
- **Ground the manifest in a Protobuf custom option at the app level only,
  with the permission list in a separate file:** rejected. The permission of an
  RPC must be at the RPC, or 1 file can disagree with another.
- **Ask the Protobuf global extension registry before writing a line of
  code:** rejected as a blocker. The registry takes time; the in-house range
  keeps the numbers provisional but unambiguous.
- **A remote BSR plugin:** rejected. It needs the network in `make generate` and
  a credential, and ADR-0085 pins the tools in `mise.lock`.

## References

- Builds on: [ADR-0001](0001-record-architecture-decisions.md) (the docs
  shape this ADR follows)
- Shapes: [design/overview.md](../design/overview.md),
  [specs/options.md](../specs/options.md),
  [specs/manifest.md](../specs/manifest.md),
  [plan 0001, phase 1](../plans/0001-roadmap.md#phase-1--options-model-manifest-and-the-gate-built-2026-11-15)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md),
  [frostyard/core ADR-0016](https://github.com/frostyard/core/blob/main/docs/adr/0016-reverse-dns-org-frostyard-identifiers.md) (the
  `org.frostyard.<app>` identifiers),
  [frostyard/core ADR-0023](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md) (the pinned
  tools)
- External: [Protobuf custom options](https://protobuf.dev/programming-guides/proto3/#options),
  [JSON Schema draft 2020-12](https://json-schema.org/draft/2020-12),
  [CloudEvents 1.0](https://github.com/cloudevents/spec/blob/v1.0.2/cloudevents/spec.md)
