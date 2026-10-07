---
title: "Define versioned offline scene planning contracts"
date: '2026-10-07'
status: accepted
summary: "Use shared backlot/v1 contracts and allocation-free planning to validate declarative scenes before runtime mutation."
tags: [contracts, configuration, planning, security]
supersedes: []
superseded_by: []
---

# Define versioned offline scene planning contracts

Use named `api/v1` project, machine and planning contracts for an offline
`backlot plan` operation. Strict decoding and complete graph validation precede
read-only seed/tool checks; allocation-dependent values remain typed symbolic
references and ordinary output redacts secrets. This makes milestone 1 useful
without pretending the daemon or providers already execute scenes.

This proposal concretizes the accepted environment/container and lifecycle
choices; it does not replace them. The [manifest guide](../manifest.md) is the
schema and command reference. Review acceptance determines schema acceptance.

## Context

The PRD requires a versioned YAML/JSON schema, explicit reusable components,
scene selection/lifetimes, dependency gates, initialization policy, owned resources
and environment layering. The scaffold has named version output but no execution
adapter. Checking a plan must not allocate resources, run application commands,
contact providers or write checkout application configuration.

The architecture requires reassessing stable YAML v4 before choosing the parser.
On 2026-10-07, primary-source upstream tag inspection (`git ls-remote --tags
https://github.com/yaml/go-yaml.git 'v4*'`) showed only v4.0.0-rc.1 through rc.6,
with no stable v4. The upstream README recommends v4 for new work but explicitly
keeps v3 API compatibility and security maintenance. Retaining the existing
BSD-licensed YAML v3 dependency avoids adding a prerelease or duplicate parser.

## Decision

Use `version: backlot/v1` and the same named Go contracts for JSON and YAML.
Decode bounded documents through YAML nodes with explicit shape/type validation,
then standard JSON decoding into those contracts. JSON input also must satisfy
standard JSON syntax. Reject unknown/duplicate/case-mismatched fields, multiple
or trailing documents, nulls, unsupported tags, anchors/aliases/merge keys, complex
keys and incorrect scalar types. Sanitize parser errors to avoid secret leakage.

References use explicit discriminators and bounded fields for declared inputs,
instance provenance, owned resources, selected services and attached outputs.
No general expression, interpolation or implicit dependency language exists.
Allocation references remain symbolic in plans; secret values carry redaction
only. Do not serialize raw component environment declarations or machine config.
Treat inherited and seed values as sensitive by default.

Validate every reusable declaration and graph, then require tools/inputs/providers
only for selected work. Explicit scene selection closes the graph; disposable
terminal jobs must depend transitively on every selected component. Preserve
fresh-only and each-start intent without inventing initialization records.
Checkout outputs carry explicit serialization groups, including overlap checks.

Layer the small launch baseline, all selected pass-through values, all literal
seed files in declaration order, then all explicit assignments in included-set
order followed by component assignments. Check required keys last. Read seeds
as literal UTF-8 KEY=value files without executing or rewriting them. Bound file
sizes and reject seed/output path escapes through existing symlinks.

Use `backlot plan <scene> [--project PATH] [--config PATH] [--json] -- <args>`.
Resolve project manifests upward only to the checkout boundary; canonicalize
paths and reject ambiguous manifest candidates. Explicit config overrides the
optional per-user default. Native tool checks inspect executable availability
without running it. Provider settings are validated locally without health checks.
Return shared `PlanResponse` or actionable `ErrorResponse`; add no HTTP endpoint
until there is a real client/server implementation.

### Editor artifacts

Publish self-contained project and machine JSON Schemas derived from these same
named Go contracts for editor completion, descriptions and local validation.
The [schema guide](../../schemas/README.md) records the chosen libraries, draft,
source annotations/hooks, regeneration, editor association and boundaries.
Committed artifacts are drift-checked in the Go-only repository checks; editor
assistance does not replace the strict decoder or graph/planning validation.

## Consequences

Projects can review selected work, environment mappings, dependencies and owned
resource intent before any external effect. JSON and YAML cannot drift into
separate models. Strict syntax deliberately excludes YAML reuse/merge features;
projects instead use named environment sets and component selections. The node
shape adapter is small security-sensitive code requiring negative tests.

Redaction makes ordinary plans unsuitable as executable snapshots containing
credentials. The future daemon must resolve and protect secrets separately while
preserving this public contract. Path provenance is not durable identity, output
groups are not acquired locks, and symbolic values are not reservations. Runtime
ownership, lease handling, readiness and cleanup remain later milestone work.

## Alternatives considered

A daemon-only first validation command makes milestone 1 unusable without
speculative transport/runtime work. Resolving values to guessed ports, URLs or
paths would give misleading evidence. General string templates expand language
and security complexity; explicit references cover the agreed use cases.

Separate YAML and JSON structs permit schema drift. JSON's default decoder alone
accepts duplicate keys and case-insensitive fields. YAML aliases and merge keys
hide conflicts and complicate bounded validation. YAML v4 remains a prerelease;
reassess when a stable release exists. A new parser dependency is unnecessary
while the existing maintained dependency meets the bounded schema requirements.

## Revisit when

Reassess YAML v4 after a stable release and compatibility/security testing.
Revisit reference shapes when runtime adapters demonstrate a concrete missing
value; preserve explicit typing rather than introducing general templating.
Revisit version negotiation when actual daemon transport is implemented.

## References

- [Manifest guide](../manifest.md)
- [Shared manifest contracts](../../api/v1/manifest.go)
- [Planning contracts](../../api/v1/plan.go)
- [Architecture](../architecture.md)
- [PRD](../prd.md)
- [Accepted generic containers and environment decision](20261007T052013841Z-declare-generic-containers-and-inject-configuration-through-environment.md)
- [Accepted lifetimes and daemon execution decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [YAML upstream repository and version intentions](https://github.com/yaml/go-yaml)
- [YAML upstream tags](https://github.com/yaml/go-yaml/tags)
