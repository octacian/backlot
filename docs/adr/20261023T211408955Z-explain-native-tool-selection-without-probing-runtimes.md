---
title: "Explain native tool selection without probing runtimes"
date: 2026-10-23
status: accepted
summary: 'Explain selected executable paths and current daemon sources through existing diagnostics without adding wire or persisted fields.'
tags: [planning, diagnostics, configuration, contracts]
supersedes: []
superseded_by: []
---

# Explain native tool selection without probing runtimes

Expose selected command/readiness paths, attempted candidates on lookup failure,
and current daemon winning sources through existing plan, error, doctor and
runtime contracts. Keep recorded paths distinct from current observations;
original successful source/candidate/context remains unrecorded. This supports
troubleshooting without new wire/state fields or runtime probing.

## Context

Offline planning uses the caller's environment; runtime planning uses the daemon's
startup environment. The planner already resolves executable paths, but loses
the winning tool layer during composition and returns generic lookup errors.
Private project overrides already exist through `project setup --edit`.
For [issue #51](https://github.com/octacian/backlot/issues/51), the owner accepts
current troubleshooting and rechecking after edits; historical reasons for a
successful selection and version/preflight recipes are deliberately deferred.

## Decision

Reuse `PlannedComponent.Executable` and `PlannedProbe.Executable` for resolved or
recorded paths in existing plan/runtime JSON and human output. Label offline
selection as caller-side and retained instance selection as recorded. A prepared
selection does not prove execution; even an absolute npm path does not identify
the child node runtime.

Keep `missing_tool` errors in existing `PlanError` fields: `Field` identifies
`tools.NAME`, `SourceLayer` the winning manifest/machine/private layer and
`SourcePath` its declaration file. Quote the attempted candidate and lookup
context in the sanitized `Message`, distinguishing command from readiness.
Preserve project-relative lookup; an unresolved bare name stays bare, with no
invented absolute result. Scene/current-instance doctor preserves this safe
failure detail instead of substituting generic caller-side plan guidance.

For successful current daemon lookup, use bounded per-selected-tool
`DoctorCheck` entries (`daemon/host`, existing statuses) for command/readiness,
candidate, winning layer/source file and resolved path; use existing `DoctorPath`
entries for absolute paths with existing filesystem states. Capture layer
membership private → machine → manifest alongside the same composition and lookup,
including equal-valued overrides. Carry observations internally on the prepared
snapshot with explicit `json:"-"`; exclude them from persisted snapshot/plan JSON,
resolved configuration and digests. Never reread sources to attribute an earlier
lookup. Do not encode metadata in digest/association fields or add enums/scopes.

Instance doctor shows recorded paths even when missing, labels availability as a
current observation, and keeps its independently prepared current selection
separate. Preserve recorded paths after edits/restart inspection; never replace
them with current lookup results or infer original source/candidate/context from
current configuration. Missing original provenance does not itself fail health.

Keep API/state versions, wire and persisted shapes, writers, execution,
precedence, drift, cleanup authority and native implementation unchanged. This
avoids strict predecessor readers rejecting unknown response fields after work
acceptance; no migration, opt-in negotiation or compatibility exception is needed.
Older daemons retain their existing diagnostic detail until explicitly restarted.

Quote displayed candidates/paths to contain control characters. Expose only tool
selection metadata, never inherited PATH/environment, private inputs, credentials
or raw lookup errors. No version execution/parsing, extra probes, recipes,
constraints, installers, environment import or new settings. Improve existing
override and data-preserving recovery guidance later in the canonical
[manifest](../manifest.md), [daemon](../daemon.md) and [operations](../operations.md)
guides; keep this record as rationale.

## Consequences

Users can compare paths and current override sources without executing extra
work. The bounded implementation reaches planner capture/errors, doctor and common
CLI presentation, with focused compatibility, privacy, nonserialization and
recorded/current distinction tests. No dependency, background work or provider
work is introduced. Successful plan/runtime JSON gains no structured source/context
value; successful original source/candidate/context history remains deferred.

## Alternatives considered

- **Documentation alone:** explains overrides but cannot show actual current
  daemon selection or attempted lookup candidates.
- **Persist richer selection values:** preserves historical reasons but adds
  wire compatibility and writer obligations beyond the approved need.
- **Reconstruct historical source on inspection:** can falsely attribute a
  recorded path after configuration changes; current observation is sufficient.
- **Version/preflight recipes:** expand execution and configuration beyond scope.

## Revisit when

Users need original successful selection reasons, machine-queryable successful
source/context, verified child runtime identity or version enforcement. Reassess
compatibility and persistence before adding those guarantees.

## References

- [Planner and tool lookup](../../internal/plan/plan.go), [configuration composition](../../internal/plan/configuration.go)
- [Snapshots](../../internal/plan/snapshot.go), [shared plan/error contracts](../../api/v1/plan.go)
- [Scoped doctor](../../internal/daemon/doctor.go), [existing doctor contracts](../../api/v1/doctor.go)
- [Shared API rules](../architecture.md#shared-api), [private configuration rationale](20261021T001732173Z-register-private-project-configuration.md)
