# Documentation

Docs are split by the question they answer:

| Directory | Question | Contents |
| --- | --- | --- |
| [adr/](adr/) | **Why** did we choose this? | Architecture Decision Records — immutable once accepted; superseded, never edited |
| [design/](design/) | **How** does it fit together? | Living documents describing the current architecture |
| [specs/](specs/) | **What exactly** is the contract? | Precise, testable interface definitions |
| [plans/](plans/) | **When/in what order** do we build? | Roadmaps and phase plans; updated as work lands |

## Index

### Decisions (ADRs)

- [0001 — Record architecture decisions](adr/0001-record-architecture-decisions.md)
- [0002 — Agent-portable instruction surface](adr/0002-agent-portable-instruction-surface.md)
- [0003 — Verify gate, risk paths and corrections](adr/0003-verify-gate-risk-paths-and-corrections.md)
- [0004 — Generate an app's manifest from hearth's Protobuf options](adr/0004-generate-the-manifest-from-protobuf-options.md)
- [0005 — Run the repository gate in Go](adr/0005-run-the-repository-gate-in-go.md)
- [0006 — Check permissions in the app, and trust lodge only on its socket](adr/0006-check-permissions-in-the-app-and-trust-lodge-only-on-its-socket.md)
- [0007 — Generate MCP tools from an app's agent RPCs](adr/0007-generate-mcp-tools-from-agent-rpcs.md)

### Org-wide decisions

- [Org-wide decisions (frostyard/core ADRs)](org-adrs.md)

### Design

- [hearth](design/overview.md)
- [Agent-ready repository](design/agent-ready-repository.md)

### Specs

- [Options](specs/options.md)
- [Manifest](specs/manifest.md)
- [Permissions](specs/permissions.md)
- [MCP tools](specs/mcp-tools.md)

### Plans

- [0001 — Roadmap](plans/0001-roadmap.md)

## Conventions

- **New docs start from their category's `TEMPLATE.md`** (in each directory).
- New decision → new ADR with the next number; if it reverses an old one, mark
  the old one `Superseded by NNNN` rather than editing it.
- Design docs are updated in place to always reflect reality.
- Specs change only alongside the code that implements them.
- Cross-links between categories are mandatory in both directions — see the
  documentation rules in [AGENTS.md](../AGENTS.md) (CLAUDE.md/GEMINI.md are
  symlinks to it, ADR-0002).
- Adding a doc means adding it to the index above.
