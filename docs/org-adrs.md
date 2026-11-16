# Org-wide decisions (frostyard/core ADRs)

Some conventions are decided at org level. They are ADRs in
[frostyard/core](https://github.com/frostyard/core/tree/main/docs/adr).

These ADRs bind this repository. The list below says what each one binds here,
and where it lives in this repository.

## Baseline decisions

- [ADR-0018 — Org-wide agent instruction and knowledge surfaces](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md) — `AGENTS.md` is the canonical instruction file; `CLAUDE.md`, `GEMINI.md`, `.cursorrules` and `.github/copilot-instructions.md` are symlinks to it. Durable corrections go in the inbox ([`.memory/README.md`](../.memory/README.md)): 1 file for each record in `.memory/corrections/`.
- [ADR-0019 — Repository governance as machine-readable policy with risk tiers](https://github.com/frostyard/core/blob/main/docs/adr/0019-governance-as-code-and-risk-tiers.md) — every pull request declares a risk tier in the risk tier section of [`.github/pull_request_template.md`](../.github/pull_request_template.md), and [`policies/agent-governance.json`](../policies/agent-governance.json) names the repository's protected boundaries.
- [ADR-0021 — SHA-pinned actions and least-privilege CI workflows](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md) — the workflows in [`.github/workflows/`](../.github/workflows/) pin every action to a full SHA with the version in a comment, set `permissions: {}`, and set `persist-credentials: false`.
- [ADR-0025 — One docs/ tree per repository, in core's four-category shape](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md) — all documentation lives in this [`docs/`](README.md) tree, in 4 categories, each with a `TEMPLATE.md`.
- [ADR-0029 — ACMM conformance via canonical aliases](https://github.com/frostyard/core/blob/main/docs/adr/0029-acmm-conformance-via-canonical-aliases.md) — the tool paths are alias symlinks, and `.claude/skills` is a symlink to `.agents/skills/`.
- [ADR-0040 — Publish the repository settings contract](https://github.com/frostyard/core/blob/main/docs/adr/0040-publish-the-repository-settings-contract.md) — [`SECURITY.md`](../SECURITY.md) states the support policy, and private vulnerability reporting is on.
- [ADR-0043 — Pin repository tools in mise and name the verify gate](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md) — [`mise.toml`](../mise.toml) and [`mise.lock`](../mise.lock) pin every tool the gate runs, and CI installs that same set.
- [ADR-0044 — Expose the make gate triad in every repository](https://github.com/frostyard/core/blob/main/docs/adr/0044-expose-the-make-gate-triad-in-every-repository.md) — the [`Makefile`](../Makefile) has the `verify`, `check` and `ci` targets.
- [ADR-0051 — ADRs state facts only, not the process that produced them](https://github.com/frostyard/core/blob/main/docs/adr/0051-adrs-state-facts-not-process.md) — the ADRs in [`adr/`](adr/) state facts only.
- [ADR-0068 — Label pull requests that touch high-risk paths](https://github.com/frostyard/core/blob/main/docs/adr/0068-label-pull-requests-that-touch-high-risk-paths.md) — [`risk-paths.json`](../.github/risk-paths.json) lists the high-risk paths, and [`risk-paths.yml`](../.github/workflows/risk-paths.yml) calls core's triage workflow, which adds `risk:high` at L1 and L2.
- [ADR-0071 — Hold every code repository to one baseline](https://github.com/frostyard/core/blob/main/docs/adr/0071-hold-every-code-repository-to-one-baseline.md) — every code repository carries the baseline checklist ([spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md)). It has a [`LICENSE`](../LICENSE), a [Dependabot config](../.github/dependabot.yml), and a [`ci.yml`](../.github/workflows/ci.yml) that runs `make ci`. It has no `CODEOWNERS`.
- [ADR-0075 — Group Dependabot updates and sync skills once a day](https://github.com/frostyard/core/blob/main/docs/adr/0075-batch-routine-pull-requests.md) — each entry of [`.github/dependabot.yml`](../.github/dependabot.yml) has 1 `patterns: ["*"]` group, so Dependabot opens at most 1 version-update PR for each ecosystem and run; security updates stay 1 PR each.
- [ADR-0076 — Require status checks on every default branch](https://github.com/frostyard/core/blob/main/docs/adr/0076-require-status-checks-on-every-default-branch.md) — the default branch requires the check `verify`, which is [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)'s 1 job.
- [ADR-0077 — Bind `ready-for-human` to the head commit](https://github.com/frostyard/core/blob/main/docs/adr/0077-bind-ready-for-human-to-the-head-commit.md) — the triage caller removes `ready-for-human` when the head moves after the label, or when the label comes while CI at the head is not green.
- [ADR-0078 — Sort pull requests by levels L1 to L4](https://github.com/frostyard/core/blob/main/docs/adr/0078-sort-pull-requests-by-levels-l1-to-l4.md) — every glob in [`risk-paths.json`](../.github/risk-paths.json) has a level, and the caller adds the label `level:L1` to `level:L4` of the strictest matched path.
- [ADR-0081 — Store each correction in a file of its own](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md) — each correction is 1 file in [`.memory/corrections/`](../.memory/README.md) in canonical form. `make verify` refuses a tree with both forms. `make ci BASE=<ref>` (CI sets the PR base) also refuses a removed or renamed record, a changed record other than a filled empty `promoted_to`, and the old single corrections file when `<ref>` has the folder.
- [ADR-0083 — Run the CI gate locally as make ci](https://github.com/frostyard/core/blob/main/docs/adr/0083-run-the-ci-gate-locally-as-make-ci.md) — `make ci` runs every check that `ci.yml` runs on a pull request; only `govulncheck` sits in a job of its own, because it needs the network.
- [ADR-0085 — Build new first-party apps on hearth, with lodge as their hub](https://github.com/frostyard/core/blob/main/docs/adr/0085-build-new-first-party-apps-on-hearth-with-lodge-as-their-hub.md) — this repository is the toolkit: the Protobuf options, the plugins and the Go runtime of a first-party app ([ADR-0004](adr/0004-generate-the-manifest-from-protobuf-options.md)). hearth imports no lodge, frostyard app or Pocket code, and [`internal/repocheck`](../internal/repocheck/) checks it.

## When they apply

Some decisions apply only when the repository gains a certain kind of code.

- [ADR-0012 — svu-derived versions, make bump, and the rolling dev prerelease](https://github.com/frostyard/core/blob/main/docs/adr/0012-svu-versioning-and-rolling-dev-prerelease.md) and [ADR-0053 — Name the svu config file `.svu.yml`](https://github.com/frostyard/core/blob/main/docs/adr/0053-name-the-svu-config-svu-yml.md) — they apply: [`.svu.yml`](../.svu.yml) is the config, and `make bump` tags with `svu next` on a clean `main`.
- [ADR-0023 — Verify pinned downloads by checksum](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md) — it applies to every tool that CI or `make ci` installs: [`mise.lock`](../mise.lock) holds a checksum and a URL for each tool and platform, and no workflow downloads a tool any other way.
- [ADR-0038 — Scope the test name filter to chairlift](https://github.com/frostyard/core/blob/main/docs/adr/0038-scope-the-test-name-filter-to-chairlift.md) — it applies: `make test` and `make verify` run `go test ./...` unfiltered, and a test that needs a live service skips behind its own variable.
- [ADR-0070 — Build tool docs sites with ProperDocs and MaterialX on GitHub Pages](https://github.com/frostyard/core/blob/main/docs/adr/0070-build-tool-docs-sites-with-materialx-on-github-pages.md) and [ADR-0084 — Accept a tool's own site or a frostyard.org guide as its docs home](https://github.com/frostyard/core/blob/main/docs/adr/0084-accept-a-tools-own-site-or-a-frostyard-org-guide-as-its-docs-home.md) — they apply: hearth is a tool that people install, so its user documentation home is [`site/`](../site/properdocs.yml), built by `make site` and deployed to <https://frostyard.github.io/hearth/> by [`.github/workflows/site.yml`](../.github/workflows/site.yml).

A Go repository uses the Go minor and the golangci-lint version in core's
`frostyard-go-repo` skill, section
[Fleet toolchain](https://github.com/frostyard/core/blob/main/.agents/skills/frostyard-go-repo/SKILL.md#fleet-toolchain):
`go.mod` names `go 1.27.4`, and [`mise.toml`](../mise.toml) pins golangci-lint
2.15.0.

When you change behavior that one of these ADRs covers, update or supersede the
ADR in frostyard/core first.
