---
title: "Define versioned offline scene planning contracts"
date: '2026-10-07'
status: superseded
implementation: implemented
summary: "Use shared backlot/v1 contracts and allocation-free planning to validate declarative scenes before runtime mutation."
tags: [contracts, configuration, planning, security]
supersedes: []
superseded_by: [/docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md]
related: []
tracking: []
implemented_by: ["https://github.com/octacian/backlot/commit/f448345fda7ed9900609b9efba512d94c303e16a"]
---

# Define versioned offline scene planning contracts

## Context and Problem Statement

Contributors need to validate a scene before starting a daemon or mutating
resources. YAML/JSON, editor schemas and runtime clients must describe the same
contracts, without leaking private inputs or guessing allocation results.

## Considered Options

- Allocation-free planning over shared named contracts.
- Daemon-only validation: makes configuration review depend on runtime setup.
- Separate YAML/JSON models or general string interpolation: introduce drift and a
  larger configuration language.
- YAML v4: prerelease at the original decision; retained maintained v3 avoids that
  compatibility risk until a stable replacement is justified.

## Decision Outcome

Use shared `backlot/v1` project, machine and planning contracts. Strict bounded
syntax/type and complete declaration/graph validation precede selected-work input,
seed and executable checks. Reject ambiguous syntax, unknown/duplicate fields and
YAML aliases/merges rather than silently resolving conflicts. Sanitize errors.

Keep allocation-dependent values as explicit typed symbolic references. Planning
never allocates resources, executes application commands, contacts providers or
rewrites application configuration. Inherited/seed values are sensitive by default;
ordinary plans redact secrets and never serialize raw private configuration.

Use deterministic explicit environment layering and literal read-only seed files,
with bounded reads and path-escape protection. Publish editor JSON Schemas from
the same named contracts; editor assistance does not replace runtime validation.
The [manifest guide](/docs/manifest.md) owns syntax and precedence.

The replacement relaxes only sensitive persistent startup presentation; offline
planning and the other contracts here remain unchanged.

### Consequences

Projects can inspect intended work before effects, but symbolic values are neither
reservations nor executable snapshots. Strict YAML deliberately excludes reuse
features. The small shape-validation adapter is security-sensitive; provider
ownership, readiness and cleanup require separate runtime guarantees.

## More Information

[Schema generation](/schemas/README.md), [shared planning contracts](/api/v1/plan.go).
