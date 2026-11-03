---
title: "Explain native tool selection without probing runtimes"
date: 2026-10-23
status: accepted
implementation: implemented
summary: 'Explain selected executable paths and current daemon sources through existing diagnostics without adding wire or persisted fields.'
tags: [planning, diagnostics, configuration, contracts]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/51"]
implemented_by: ["https://github.com/octacian/backlot/commit/4a35ed47018c17aaaa076b4b2b32edf2f727b1a2"]
---

# Explain native tool selection without probing runtimes

## Context and Problem Statement

Offline selection uses the caller environment while runtime planning uses the
daemon environment. Existing paths and generic errors do not explain the winning
tool layer; reconstructing historical provenance after edits would mislead users.
The selected need is current troubleshooting, not version enforcement or a new
persisted provenance model.

## Considered Options

- Reuse existing plan/error/doctor fields with private current lookup observations.
- Documentation alone: cannot show actual daemon selection or attempted candidates.
- Persist richer successful-selection provenance: adds wire and writer obligations.
- Infer historical sources or execute version probes: invents facts or expands work.

## Decision Outcome

Show resolved/recorded executable paths through existing contracts, explicitly
labelled caller-side, recorded or current daemon observations. Missing-tool errors
identify the winning source and sanitized attempted candidate/context. A prepared
path proves neither execution nor the identity of a child runtime.

Capture successful current source attribution with the same composition/lookup,
including equal-valued overrides. Keep it private and nonserialized rather than
adding wire/state fields or encoding facts into digest/association fields. Recorded
paths remain separate after edits; absent original provenance is unknown and does
not itself fail health.

Quote candidates/paths to contain control characters. Never expose inherited PATH,
private inputs, credentials or raw errors. Preserve precedence, execution, drift
and cleanup authority without installers, version execution or extra runtime probes.

### Consequences

Existing diagnostics explain useful current facts without compatibility changes,
new dependencies or background work. Original successful source/candidate/context
history remains unavailable. Revisit storage and compatibility only when an actual
consumer needs that history, structured provenance or verified runtime versions.

## More Information

[Tool configuration](/docs/manifest.md), [scoped doctor](/internal/daemon/doctor.go).
