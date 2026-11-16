# 0009 — Keep the example app a module of its own, built from its README

- **Status:** Accepted
- **Date:** 2026-11-16

## Context

hearth is open source first: a developer outside frostyard must be able to
build an app on it from its documentation alone (frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
The demo app of `internal/e2e` proves hearth's own rules, but it is a package
of hearth's module, so it can import `internal/` packages that no outside
module can, and its plugins run as `./cmd/...` of this checkout. These facts
hold:

- **A tutorial that is not tested goes stale.** Each change of an option, of a
  plugin or of a runtime signature can break a step that nobody runs.
- **An outside module sees only hearth's public packages.** Go refuses an
  import of another module's `internal/` package, so only a module of its own
  proves that an app needs nothing else.
- **The options file has no registry home yet.** No Buf Schema Registry module
  publishes `frostyard/hearth/v1/options.proto`, and hearth runs no remote
  plugin or module (AGENTS.md). The file is in hearth's Go module, so the Go
  module cache holds it at the version that `go.mod` requires.
- **`go run` of a package pins the plugin by `go.sum`.** In an app's module,
  `go run github.com/frostyard/hearth/cmd/protoc-gen-hearth-authz` builds the
  plugin at the hearth version that the app requires. In hearth's own checkout,
  a `replace` makes that this repository's own command.

## Decision

- **`examples/notes-lite` is a Go module of its own,** `example.com/notes-lite`,
  which requires `github.com/frostyard/hearth`. Its `go.mod` adds 1 line that an
  outside app does not have: `replace github.com/frostyard/hearth => ../..`, so
  that it builds against this checkout.
- **Its README is the tutorial, and the tutorial is its source.** Each file that
  a developer writes by hand is 1 code block of
  `examples/notes-lite/README.md`, marked with its path. A test of hearth's gate
  fails when a block differs from its file, or when a hand-written file of the
  example has no block. `go.mod`, `go.sum`, `buf.lock`, the vendored options
  file and `gen/` come from the README's commands, not from a block.
- **The options file is vendored by a command.** The README copies
  `proto/frostyard/hearth/v1/options.proto` from the module cache
  (`go list -m -f '{{.Dir}}' github.com/frostyard/hearth`) into
  `third_party/`, a module of the app's own `buf.yaml`. A test of hearth's gate
  fails when the example's copy differs from hearth's file.
- **The plugins run with `go run` of hearth's commands.** The example's
  `buf.gen.yaml` runs `protoc-gen-go` from the developer's tools and each hearth
  plugin as `go run github.com/frostyard/hearth/cmd/<plugin>`, so the plugin is
  the version of the app's `go.mod`, and no plugin is fetched from a registry.
  The app records each plugin and `cmd/hearth` as a `tool` of its `go.mod`
  (`go get -tool`), so that `go mod tidy` keeps the packages they import in
  `go.sum`.
- **The example has its own Makefile and gate.** `make ci` of the example runs
  its tidy check, vet, lint, `buf` lint and format, its tests, its
  generated-code check and its manifest diff against `BASE`. hearth's `make ci`
  runs the example's gate, so a change of hearth that breaks the tutorial fails
  hearth's own gate.

## Consequences

- An outside developer copies the README's blocks and runs its commands, and
  gets the example: hearth's CI proves it on every change.
- The example's README is long: it holds every file a developer writes.
- The example is 1 more `go.mod`, so it has its own Dependabot entry, and its
  dependencies (the SQLite driver) are its own.
- A change of a runtime signature changes the README in the same pull request,
  because the test reads both.
- The vendored options file is a copy, and the copy test is what keeps it
  equal. An app outside hearth copies it again when it moves to a new hearth
  version.

## Alternatives considered

- **The example as packages of hearth's module:** rejected. It could import
  `internal/` packages without a failure, and its import paths would be
  hearth's, not an outside module's.
- **A separate repository for the example:** rejected. A change of hearth could
  break it without a failure in hearth's own gate.
- **A Buf Schema Registry module for the options:** deferred. hearth fetches no
  remote module, and the extension numbers are in-house until the Protobuf
  global extension registry grants 1.
- **Snippets in the README, and the full source only in the tree:** rejected. A
  developer could not build the app from the README alone, and nothing would
  keep the snippets equal to the code.

## References

- Shapes: [design/overview.md](../design/overview.md),
  [plan 0001, phase 3](../plans/0001-roadmap.md#phase-3--events-transports-and-the-example-app-built-2026-11-16)
- Builds on: [ADR-0004](0004-generate-the-manifest-from-protobuf-options.md)
  (the options and the manifest),
  [ADR-0005](0005-run-the-repository-gate-in-go.md) (the gate's checks in Go),
  [ADR-0008](0008-publish-thin-events-through-a-transactional-outbox.md)
  (the events the example publishes)
- Builds on: [frostyard/core ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)
