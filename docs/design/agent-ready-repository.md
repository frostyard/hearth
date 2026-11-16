# Agent-ready repository

Living document. Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md), [ADR-0002](../adr/0002-agent-portable-instruction-surface.md), [ADR-0003](../adr/0003-verify-gate-risk-paths-and-corrections.md), [ADR-0005](../adr/0005-run-the-repository-gate-in-go.md), [core ADR-0081](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md).  
Contracts: [repository baseline spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md), [README spec](https://github.com/frostyard/core/blob/main/docs/specs/readme.md).

## Overview

This repository keeps the structure of [bketelsen/agentic-template](https://github.com/bketelsen/agentic-template): a portable instruction surface, category-based documentation, reusable skills, a verification gate and a corrections loop, for work that LLM agents build and maintain. The structure is extracted from [bespoke](https://github.com/bketelsen/bespoke), where it has held up across coding agents. hearth fills it with Go code, and its gate runs in Go ([ADR-0005](../adr/0005-run-the-repository-gate-in-go.md)).

## Design

Agents succeed when the repository answers their questions before they ask:

- **One law, every agent.** `AGENTS.md` is the canonical instruction file; `CLAUDE.md`, `GEMINI.md`, `.cursorrules` and `.github/copilot-instructions.md` are symlinks to it, so Claude Code, Copilot, Gemini, Cursor and whatever comes next all read the same rules. Skills live once in `.agents/skills/` (`.claude/skills` is a symlink). No drift, ever.
- **Docs split by the question they answer.** `docs/adr/` (why — immutable), `docs/design/` (how — living), `docs/specs/` (what exactly — testable contracts), `docs/plans/` (when — phases with "Done when" outcomes). An agent that needs rationale, mechanism, contract or sequence knows exactly where to look, and where to write.
- **Templates as enforcement.** Every category has a `TEMPLATE.md`; every new doc starts from it. Structure you do not have to think about is structure that stays consistent.
- **Mandatory cross-linking.** ADRs link the designs they shape; designs link their ADRs, specs and roadmap phase; specs link back. A doc without its links is incomplete, so the graph stays navigable from any entry point.
- **Live conventions only.** The code-conventions section of `AGENTS.md` describes the code as it is: a rule graduates in when the enforcing code lands. Agents following stale rules produce broken work; this structure makes a stale rule a category error.
- **Skills over improvisation.** A multi-step procedure that repeats becomes `.agents/skills/<name>/SKILL.md` with a frontmatter description that agents select on. Explaining a procedure twice means it should become a skill.
- **A Go gate protects the rules.** `make verify` runs the repository's own Go tests and the pinned tools: the workflows, the risk-path list, the corrections inbox, the Dependabot entries, the buf configuration, the documentation links and the import boundary. The risk-path list gives every path a level, and the caller labels pull requests with it; the corrections inbox turns a blocking review into a guard.

**Layout.** The repository's parts are:

| Path | What it is |
| --- | --- |
| `AGENTS.md` | Canonical agent instructions (the law) |
| `CLAUDE.md` → `AGENTS.md` | Symlink for Claude Code |
| `GEMINI.md` → `AGENTS.md` | Symlink for Gemini CLI |
| `.cursorrules` → `AGENTS.md` | Symlink for Cursor |
| `.github/copilot-instructions.md` → `AGENTS.md` | Symlink for Copilot |
| `.agents/skills/` | Canonical skills; `TEMPLATE/SKILL.md` copies for new skills |
| `.claude/skills` → `.agents/skills` | Symlink |
| `docs/README.md` | Taxonomy and index; every doc gets a line |
| `docs/org-adrs.md` | Core ADRs that bind this repository |
| `docs/adr/`, `docs/design/`, `docs/specs/`, `docs/plans/` | The 4 doc categories, each with `TEMPLATE.md` |
| `Makefile` | The gate triad, the generated-code check and the site |
| `mise.toml`, `mise.lock` | Tool pins; `mise install` provisions them |
| `policies/agent-governance.json` | The protected boundaries, which the risk-path list names |
| `proto/frostyard/hearth/v1/options.proto` | The 3 hearth options |
| `gen/go/` | The options' generated Go code, committed |
| `internal/model/` | The app model, and every rule of the options |
| `manifest/`, `schema/` | The manifest's types, its diff and its JSON Schema |
| `cmd/protoc-gen-hearth-manifest/` | The manifest plugin, with its golden and failure cases |
| `cmd/hearth/` | The CLI: `hearth manifest diff` and `validate` |
| `internal/workflowcheck/`, `internal/repocheck/` | The tests of the repository's own rules |
| `internal/cmd/corrections-base/` | The check that a correction record is permanent |
| `site/` | The user documentation site |
| `.memory/` | Corrections inbox: 1 file for each record in `.memory/corrections/` |
| `.github/risk-paths.json` | High-risk repository paths, with a level for each |
| `.github/dependabot.yml` | Dependency update configuration |
| `.github/pull_request_template.md` | Risk tier and verification checklist |
| `.github/workflows/` | CI, the site and the PR triage caller |

## Operational notes

`mise install` provisions the pinned tools: golangci-lint, buf, protoc-gen-go and svu. Go comes from `go.mod`'s `go` line; `mise.toml` sets `idiomatic_version_file_enable_tools = ["go"]` so that `mise` reads it. `make verify` needs only Go, `make` and those 4 tools; it installs nothing and uses no package manager. Symlinks require Linux or macOS, or `core.symlinks=true` on Windows. GitHub's web renderer shows a symlink as its target path; this is cosmetic only.

The gate checks the canonical surfaces, the documentation index, the risk paths, the corrections inbox, the buf configuration and the import boundary. A pull request that matches a glob of `.github/risk-paths.json` at L1 or L2 gets `risk:high`; core takes the level from the matched globs, `default_level` and the org floors, and it never lowers a level label that the pull request already has. A blocking review becomes a targeted test or an `AGENTS.md` rule, with 1 record file in `.memory/corrections/`.

`make ci` runs `make verify` plus the generated-code check, the race detector, 4 cross-builds and the corrections check against `BASE`. CI runs the same target in 1 job, and `govulncheck` in a second job because it needs the network.

## References

- Rationale: [ADR-0001](../adr/0001-record-architecture-decisions.md), [ADR-0002](../adr/0002-agent-portable-instruction-surface.md), [ADR-0003](../adr/0003-verify-gate-risk-paths-and-corrections.md), [ADR-0005](../adr/0005-run-the-repository-gate-in-go.md), [core ADR-0081](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md)
- Contracts: [repository baseline spec](https://github.com/frostyard/core/blob/main/docs/specs/repository-baseline.md), [README spec](https://github.com/frostyard/core/blob/main/docs/specs/readme.md)
