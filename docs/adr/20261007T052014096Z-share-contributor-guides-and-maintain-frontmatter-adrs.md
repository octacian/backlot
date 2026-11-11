---
title: "Share contributor guides and maintain frontmatter ADRs"
date: 2026-10-07
status: accepted
implementation: implemented
summary: "Keep engineering rules in shared guides and validate timestamped frontmatter ADRs with Go-only tooling."
tags: [documentation, conventions, tooling]
supersedes: []
superseded_by: []
related: []
tracking: []
implemented_by: ["/Makefile", "/AGENTS.md", "/docs/adr/README.md"]
---

# Share contributor guides and maintain frontmatter ADRs

## Context and Problem Statement

Duplicated agent-specific rules drift. The local ADR template also encourages
implementation specifications and repeated summaries, making decisions expensive
to read. A bespoke format/checker creates maintenance without improving rationale.

## Considered Options

- Tighten the local template/checker: retains a second format implementation.
- Adopt the public BSD-licensed Corestack checker and MADR-based format: removes
  local tooling while retaining metadata and lifecycle validation.
- Separate agent manuals or JavaScript tooling: duplicate policy or add a runtime.

## Decision Outcome

Keep human and agent expectations in shared contributor guides, with `AGENTS.md`
as the entry point. Adopt the pinned Corestack Go checker as the sole record-format
and template owner; the project owns its version pin and documentation policy.
Generate records with that tool and validate them in dedicated documentation CI.

ADRs retain consequential choices, rationale, meaningful alternatives and
consequences. Delivery scope, steps and validation belong in issues/plans, execution
evidence in PRs, and current contracts/procedures in maintained guides or code.
Do not require a duplicate overview, implementation inventory or delivery checklist.
Acceptance belongs to the owner and remains independent of delivery state.

### Consequences

One policy serves contributors and agents, and local checker/template maintenance
is removed. Checks now depend on availability of a pinned public Go module hosted
outside this repository. Structural validation cannot judge decision quality,
approval or whether implementation evidence is sufficient; review still must.

## More Information

[Documentation policy](/docs/adr/README.md), [contributor checks](/docs/testing.md#commands),
[public format specification](https://gitea.auvem.com/corestack/go/src/branch/main/docs/docs-format.md).
