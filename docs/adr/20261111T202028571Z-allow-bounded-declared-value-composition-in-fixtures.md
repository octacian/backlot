---
title: "Allow bounded declared value composition in fixtures"
date: 2026-11-11
status: accepted
implementation: implemented
summary: "Extend existing bounded composition to fixtures without widening reference permissions or sensitive disclosure."
tags:
  - manifest
  - planning
  - security
related: [/docs/adr/20261104T030614332Z-compose-bounded-declared-values-for-explicit-consumers.md, /docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md, /docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
supersedes: [/docs/adr/20261104T030614332Z-compose-bounded-declared-values-for-explicit-consumers.md]
superseded_by: []
tracking: ["https://github.com/octacian/backlot/issues/138"]
implemented_by: ["https://github.com/octacian/backlot/pull/139"]
---

# Allow bounded declared value composition in fixtures

## Context and Problem Statement

Declared startup details select component fixtures, but fixtures exclude composition.
A project cannot directly declare a complete
`https://<scene-host>/api/dev/direct-login/<secret>` fixture from its published
origin and secret. Planner and daemon fixture resolution already share the value
composition machinery; the exclusion requires projects to retain assembly wrappers.

## Considered Options

- Allow all existing composition forms in fixtures using shared value resolution.
- Allow only URL composition: meets this example but creates another consumer-specific
  subset and excludes existing formatting and endpoint/path construction.
- Keep wrappers: avoids a contract change but retains application-owned assembly
  solely to display a declared value.

## Decision Outcome

Replace only the fixture composition exclusion in the
[bounded-value decision](/docs/adr/20261104T030614332Z-compose-bounded-declared-values-for-explicit-consumers.md).
Allow `format`, `host_port`, `url_path` and `file_path`, including nested operands,
as fixture values alongside existing literals and references. Preserve every other
choice in that decision, including image restrictions, constructor semantics,
compatibility, provenance, document/depth bounds and the 1 MiB rendered-output
limit on each composition, checked offline when known and after hydration before use.
Fixtures remain native consumers, including filesystem path semantics, regardless
of the declaring component's runtime.

Validate every operand under existing fixture reference permissions, even when
construction later removes its text. Preserve attachment, scene selection and
origin publication checks; CA certificate paths and build-image references remain
forbidden in fixtures at every nesting level. Composition grants no additional
reference authority, allocation, trust or publication exemption.

Sensitivity is the union of the outer value and every operand. Redact the whole
sensitive fixture in ordinary plans, results and discovery; retain its operands
and hydrated value only through existing private capture, with sanitized failures.
The [startup disclosure decision](/docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
and [attempt-selection successor](/docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md)
continue to govern selected successful persistent-start display, explicit named-secret
retrieval, attempt pinning, unavailable summaries and all ordinary-output boundaries.
This adds no disclosure permission. Application authentication remains project-owned.

### Consequences

Projects can declare a complete sensitive startup URL using existing constructors
and fixture selection. Such selected startup stdout/JSON intentionally contains the
credential and needs the same protection as existing sensitive startup details.
Shared validation and resolution avoid a second value language; root-only context
checks must cover nested fixture operands. Existing fixture capture and retention
remain the only writers and storage lifecycle. No new endpoint, persistence format,
migration, dependency or background work is required; unused composition adds no
ongoing work and existing literal/reference behavior remains unchanged.
