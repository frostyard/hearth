# 0005 — Run the repository gate in Go

- **Status:** Accepted
- **Date:** 2026-11-15

## Context

The template that hearth started from runs its gate with Node.js: 2 scripts
under `scripts/` and 2 test files under `test/`
([ADR-0003](0003-verify-gate-risk-paths-and-corrections.md)). hearth is a Go
repository: its code, its plugins, its tests and its generated code are Go, and
its fleet rules pin 1 Go minor and 1 golangci-lint release
([frostyard/core ADR-0043](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md)).

A Node gate costs a second toolchain in `mise.toml` and `mise.lock`, a second
test runner, and a second set of reviewers' habits. It also makes the
repository's own checks the only Node code in a Go repository, which no
contributor will maintain.

## Decision

The repository's gate is Go, and the gate needs only Go and the tools that
`mise.toml` pins: golangci-lint, buf, protoc-gen-go and svu.

- `make verify` runs `go mod tidy -diff`, `go vet ./...`, a gofmt check,
  golangci-lint at the release that `mise.toml` pins, `buf lint`,
  `buf format --diff --exit-code` and `go test ./...`.
- `make ci` adds the generated-code check (`make gen-check`), the race
  detector, 4 cross-builds and the corrections check against `BASE`.
- `internal/workflowcheck` and `internal/repocheck` are Go tests: they check
  the workflows, the risk-path list, the corrections inbox, the Dependabot
  entries, the buf configuration, the documentation links and the import
  boundary.
- `internal/cmd/corrections-base` is a small Go program behind
  `make corrections-base`. It reads `git diff --name-status -M` and
  `git show`, so a record file cannot be deleted, renamed, or changed in a
  field other than a filled empty `promoted_to`.
- The Node scripts, the Node tests and the template's
  `docs/new-repository.md` are removed. No file of the repository runs Node.js.

## Consequences

- 1 toolchain: `mise install` and Go are all a contributor needs for the gate.
  The documentation site still needs Python 3, and only `make site` uses it.
- A gate rule is a Go test, so it appears in the same test run as the code, and
  a contributor can debug it with the Go tools they already use.
- The gate's rules are the repository's own code now, so they are reviewed like
  code, and a change to them is a change at L2 (`.github/risk-paths.json`).
- The template's Node scripts no longer arrive with a template update. A later
  fix of a gate rule is a change in this repository, not a copy from core.
- `make verify` needs the Go module cache for `buf`'s and golangci-lint's
  binaries only; no test uses the network.

## Alternatives considered

- **Keep the Node gate and add the Go checks beside it:** rejected. 2
  toolchains, 2 test runners, and the Node part would still be unwritten in the
  repository's own language.
- **Vendor core's Node scripts and run them from the Makefile:** rejected.
  Vendoring needs a hash pin and a copy step (ADR-0003), and it keeps Node in
  `mise.toml` for 1 repository that is otherwise Go.
- **Run the checks with a `go run` of core's tools:** rejected. core's tools are
  Node programs.

## References

- Supersedes in part: [ADR-0003](0003-verify-gate-risk-paths-and-corrections.md)
  (the Node gate and the copied `scripts/`)
- Shapes: [`Makefile`](../../Makefile),
  [design/agent-ready-repository.md](../design/agent-ready-repository.md),
  [internal/workflowcheck/](../../internal/workflowcheck/),
  [internal/repocheck/](../../internal/repocheck/),
  [internal/cmd/corrections-base/](../../internal/cmd/corrections-base/)
- Builds on: [frostyard/core ADR-0022](https://github.com/frostyard/core/blob/main/docs/adr/0022-make-ci-gate-and-test-naming-filter.md),
  [frostyard/core ADR-0043](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md),
  [frostyard/core ADR-0044](https://github.com/frostyard/core/blob/main/docs/adr/0044-expose-the-make-gate-triad-in-every-repository.md),
  [frostyard/core ADR-0083](https://github.com/frostyard/core/blob/main/docs/adr/0083-run-the-ci-gate-locally-as-make-ci.md)
