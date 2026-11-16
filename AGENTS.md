# frostyard/hearth

hearth is a toolkit for frostyard's first-party apps: Protobuf options, `protoc`
plugins and a Go runtime, so that 1 Protobuf service gives an app its API, its
MCP tools, its permission checks, its events and a versioned manifest
(frostyard/core
[ADR-0085](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md)).
lodge is the hub of those apps: it issues access, routes MCP and brokers
events. Start at [docs/README.md](docs/README.md). The design is in
[docs/design/overview.md](docs/design/overview.md), the options contract in
[docs/specs/options.md](docs/specs/options.md), and the manifest format in
[docs/specs/manifest.md](docs/specs/manifest.md).

This file (`AGENTS.md`) is the canonical agent instructions. `CLAUDE.md`,
`GEMINI.md`, `.cursorrules` and `.github/copilot-instructions.md` are symlinks
to it, and `.claude/skills` symlinks to `.agents/skills/`
([ADR-0002](docs/adr/0002-agent-portable-instruction-surface.md)). Edit only the
canonical paths; keep content tool-agnostic.

## Status

Incubating and pre-release. The repository is a frostyard/core
[Plan 0002](https://github.com/frostyard/core/blob/main/docs/plans/0002-org-portfolio-roadmap.md#incubating-repositories)
repository: its decision date is 2027-03-31. Phase 1 is built: the options,
the model, the manifest plugin, the schema, the CLI, the Go gate and the docs
site. The permission runtime, the lodge adapter, the MCP plugin, the events
plugin and the runtime are later tasks of the
[roadmap](docs/plans/0001-roadmap.md); do not build them early.

## Map

- `proto/frostyard/hearth/v1/options.proto`: the 3 options, and nothing else.
  The extension numbers 51370 to 51379 are hearth's in-house range until the
  Protobuf global extension registry grants a number.
- `gen/go/`: the options' generated Go code (`hearthv1`), committed. Never edit
  it by hand.
- `internal/model`: the app model that every plugin reads, with every rule of
  [docs/specs/options.md](docs/specs/options.md).
- `manifest/`: the format's Go types, its canonical JSON encoder, its reader
  and its compatibility rules (`Diff`, `NextMajor`).
- `schema/`: `manifest-v1.json`, embedded, and `Validate`.
- `cmd/protoc-gen-hearth-manifest`: the plugin, with golden and failure cases
  under `testdata/`.
- `cmd/hearth`: the CLI, `hearth manifest diff` and `hearth manifest validate`.
  The standard library `flag` package only; no cobra.
- `testdata/e2e/`: a demo app, its buf template and its committed manifest.
- `internal/workflowcheck`: the repository's own rules as Go tests: the
  workflows, the risk-path list, the corrections inbox, the Dependabot entries
  and the buf configuration.
- `internal/repocheck`: the docs links and index, the private-host boundary, and
  the import boundary.
- `internal/cmd/corrections-base`: the check behind `make corrections-base`.
- `site/`: the user documentation site (ProperDocs and MaterialX, core
  ADR-0070).

## Gates

- `make ci` is the command to run before a push: `verify gen-check race cross
  corrections-base`. `make check` formats first, then verifies. `mise install`
  provisions golangci-lint, buf, protoc-gen-go and svu; Go comes from `go.mod`.
- `make verify` runs `go mod tidy -diff`, `go vet ./...`, a gofmt check, the
  pinned golangci-lint, `buf lint`, `buf format --diff --exit-code` and
  `go test ./...`.
- `make generate` writes `gen/` and the demo app's `manifest.json`; `make
  gen-check` regenerates both into a temporary directory, compares that
  directory with the tree, and fails on a changed, a missing or an extra file,
  then sweeps the directory away. Run `make generate` after a change of the
  options, of the model or of the plugin, and commit the result with the
  change.
- Tests run unfiltered (`go test ./...`, frostyard/core ADR-0038). A test uses
  a temp dir, and never sleeps, uses the network or the wall clock.
- `make vulncheck` needs the network, so it is in no gate; CI runs it as its
  own job.
- Fix a lint finding rather than loosening `.golangci.yml`.

## Invariants

- **hearth imports no lodge, frostyard app or Pocket code**, in Go or in
  Protobuf (core ADR-0085). `internal/repocheck` fails on it.
- **The options are fixed.** The option file holds the 2 enums, the 2 messages
  and the 3 extensions that [docs/specs/options.md](docs/specs/options.md)
  names, and nothing else.
- **A generated file is committed.** `gen/` and every golden manifest are in
  the tree, and `make gen-check` fails on a difference without reading git: it
  regenerates into a temporary directory and compares the 2 trees.
- **No remote buf plugin runs.** Every plugin is a local binary that
  `mise.lock` pins, or `go run` of this repository's own command.
- **Tools are pinned in `mise.toml` and `mise.lock`**, with a checksum for each
  platform. No workflow downloads a tool any other way.
- **`buf.yaml` and `buf.lock` are 1 pair.** `buf.lock` is in the tree, and
  `buf dep update` keeps it; a dependency that `buf.yaml` declares appears in it
  with a digest. The buf test fails when `buf.yaml` exists and `buf.lock` does
  not.
- **The manifest is the contract.** A change of the format is a change of
  `schema/manifest-v1.json`, of the spec and of the code in 1 pull request, and
  a breaking change of the format changes its major version.
- **No private hosts.** Docs write `example.com`, and never a tailnet, device or
  home-network name or a real address.
- The 8 `.memory/corrections/*.json` files of the template are permanent.

## Releases

- **Who tags: Brian.** Agents never tag, push tags or release. A due release
  goes to Brian as an ask.
- `make bump` is the only way to push a `vX.Y.Z` tag. It runs on `main` when the
  tree is clean and equal to `origin/main`, runs `make check`, takes the version
  from `svu next`, and pushes an annotated tag. svu reads only `vX.Y.Z` tags
  (`.svu.yml`), so the first release is `v0.1.0`.
- The series is `vX.Y.Z`, from semver on the changes since the last tag. There
  is no library series: `manifest/` and `schema/` are Go packages of this
  module, used through the module.
- The tags are the record of each release, so this file names no latest
  version. The newest 1 is the first line of
  `git ls-remote --tags --refs --sort=-v:refname origin 'refs/tags/v[0-9]*'`,
  or GitHub > frostyard/hearth > Tags.

## Repository baseline

The repository follows core's baseline (frostyard/core ADR-0071, its spec
`docs/specs/repository-baseline.md`, rows B1 to B19;
[docs/org-adrs.md](docs/org-adrs.md)). `internal/workflowcheck` and
`internal/repocheck` fail `make verify` when one of these breaks:

- The instruction symlinks above (B1). Edit `AGENTS.md`, never a link.
- `.memory/corrections/` (B2, core ADR-0081): 1 file for each correction record,
  named `<date>-<slug>.json` (from a pull request:
  `<date>-pr-<N>-<words>.json`), with exactly `date`, `scope`, `correction`,
  `evidence` and `promoted_to`, in canonical form: the text of
  `JSON.stringify(record, null, 2)` and 1 final newline, escapes and lone
  surrogates included. A record is permanent: never
  remove or rename a file, and change only an empty `promoted_to`. A fix of a
  blocking review point adds its guard and 1 record file.
- `.github/risk-paths.json` (B6, core ADR-0068, ADR-0078): schema version 2,
  each glob at `L1` to `L4`, the default `L3`, and each entry at L1 or L2 names
  a protected boundary of `policies/agent-governance.json` before its colon.
  The manifest plugin, `manifest/` and `schema/` are at L2: the manifest's
  capabilities are what lodge's consent page shows, and the diff decides a new
  consent.
  A glob that matches no file yet is allowed, because a later task adds those
  paths. A release workflow, install script, unit, credential store, auth check
  or socket admission joins the list in the pull request that adds it.
- `.github/dependabot.yml` (B13): 1 entry for each manifest (`go.mod`), each
  with exactly 1 `patterns: ["*"]` group.
- Every `uses:` is a 40-character commit SHA with a version comment, every
  workflow has `permissions: {}` at the top, and every checkout sets
  `persist-credentials: false` (B5).
- `README.md` follows core's README spec for the kind `tool` (B17).
- No `CODEOWNERS` file (B15), and no other agent framework's tree (B16).

## Documentation rules (enforced)

Docs live in `docs/` in 4 categories (frostyard/core ADR-0025). Every new doc
starts from its category's `TEMPLATE.md` and follows its structure:

- `docs/adr/`: why we decided. Immutable once Accepted; a reversal is a new ADR
  that marks the old one Superseded. While the repository is pre-release, a
  Proposed ADR may still be edited in place.
- `docs/design/`: how it fits together. Living; updated in place to match
  reality.
- `docs/specs/`: exact contracts. Change only alongside the implementing code.
- `docs/plans/`: order of work. Phases with "Done when" outcomes.

### Cross-linking is mandatory

A doc without its required links is incomplete; do not finish a docs change
until they exist, in both directions:

- **ADR** links every design doc and spec it shapes, and the prior ADRs it
  builds on.
- **Design doc** links the ADR(s) providing its rationale, the spec(s) pinning
  its contracts, and the roadmap phase that builds it.
- **Spec** links its motivating ADR(s) and the design doc showing where it fits.
- **Plan** links every design doc and spec that a phase implements.

Use relative paths; `make check` fails on a relative link whose file or heading
is missing.

### Housekeeping

- New doc: add a line to the index in [docs/README.md](docs/README.md).
- New significant decision: new ADR first, then the affected design docs and
  specs in the same change.
- Convert relative dates ("next weekend") to absolute dates in all docs.
- A change to user-visible behavior updates the affected page in `site/content/`
  in the same pull request.
- A new command, option or manifest member updates the affected `site/` page in
  the same pull request.
