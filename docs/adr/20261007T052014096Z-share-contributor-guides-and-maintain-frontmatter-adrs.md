---
title: "Share contributor guides and maintain frontmatter ADRs"
date: 2026-10-07
status: accepted
summary: "Keep engineering rules in shared guides and validate timestamped frontmatter ADRs with Go-only tooling."
tags: [documentation, conventions, tooling]
supersedes: []
superseded_by: []
---

# Share contributor guides and maintain frontmatter ADRs

Humans and coding agents use shared engineering guides. AGENTS.md is a reading map. Consequential choices use timestamped ADRs with searchable frontmatter and Go-only creation/validation tooling.

## Context

Duplicated tool-specific instructions drift. Decisions need discoverable rationale and safe supersession without manual numbering. Repository tooling should not add Node to a Go project.

## Decision

Document package grouping, DRY reuse, public contracts, narrow interfaces, and owned background work in shared guides. Require formatting, vet, pinned golangci-lint, tests/race checks, builds, and ADR validation.

Require ADR title/date/status/summary/tags/supersession metadata, an actionable overview, and supporting sections. Preserve accepted rationale through reciprocal supersession links and reject cycles. Create exclusively with UTC millisecond collision handling. Decision acceptance is independent of implementation progress.

## Consequences

Standards remain consistent across contributors/tools. The creator/validator requires maintenance but reuses YAML, Markdown, and Unicode libraries. Automation cannot judge rationale quality or approval.

## Alternatives considered

Agent-specific copies create competing standards. Sequential ADR numbers collide. JavaScript tooling adds an unwanted development runtime. A handwritten Markdown parser adds avoidable format-handling code.

## Revisit when

An established Go ADR tool that preserves these metadata/lifecycle contracts with less maintained code would justify replacement.

## References

- [Developer documentation](../README.md)
- [ADR policy](README.md)
- [ADR tooling](../../internal/adr/adr.go)
- [Checks](../testing.md#commands)
