---
title: "Explain native tool selection without probing runtimes"
date: 2026-10-23
status: proposed
summary: 'Expose native executable selection and its source and context so users can diagnose tool mismatches without runtime probing.'
tags: [planning, diagnostics, configuration, contracts]
supersedes: []
superseded_by: []
---

# Explain native tool selection without probing runtimes

Expose the attempted candidate, resolved executable, winning configuration layer
and lookup context for selected native commands and command readiness checks.
Carry recorded selection through existing plan, doctor and runtime diagnostics;
report missing historical provenance as unrecorded. This explains launcher
selection without claiming to identify the runtime launched indirectly by a script
or package manager.

## Context

Offline planning uses the caller's environment; runtime planning uses the daemon's
startup environment. The planner already resolves executable paths, but loses
the winning tool layer during composition and returns generic lookup errors.
Private project overrides already exist through `project setup --edit`.
[Issue #51](https://github.com/octacian/backlot/issues/51) is narrowed to explaining
this existing selection, rather than managing tool versions.

## Decision

Use a small named `api/v1` selection value in existing results/errors, with tool
name, attempted executable, optional resolved path, winning layer (manifest,
machine or private project), and caller/offline or daemon/runtime context.
Distinguish component command from readiness selection. Reuse the current
composition and lookup path; preserve precedence and availability checks.

On lookup failure, expose the attempted candidate in typed and human diagnostics.
Relative paths retain existing project-relative resolution; unresolved bare names
remain bare and have no resolved path. Scene doctor preserves this failure detail
instead of directing users only to a possibly different caller-side plan.

Capture successful selection alongside the existing launch plan, retaining its
executable fields as the execution inputs. Instance doctor and runtime status use
recorded launch facts, even after files or PATH change; any current observations
remain distinct. Never infer an older record's layer or context from current
configuration. Its existing executable path remains usable with provenance
unrecorded. New selection metadata must not affect drift digests, execution,
cleanup authority or historical acceptance.

Optional metadata is mechanically readable by current JSON state readers, but
its classification under the [state migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
is an explicit review checkpoint before implementation. If it requires a format
change, capture the unmodified predecessor and retain the migration/fixture path;
do not silently add that obligation or rewrite historical records to invent facts.

Expose only selection metadata, never inherited PATH, environment values, private
inputs or credentials. Do not add version execution/parsing, probes, recipes,
constraints, installers, automatic environment import, or another settings system.
Usage belongs in the [manifest](../manifest.md), [daemon](../daemon.md) and
[operations](../operations.md) guides; improve override and recovery discoverability
there and link to this rationale.

## Consequences

Users can distinguish caller/daemon mismatches and override sources without
executing extra work. Shared typed results, error propagation, snapshot writers,
client validation and CLI presentation need focused coverage. No background work,
dependency or manifest/machine/private document schema change is expected.
Historical gaps remain visible, and a selected launcher does not prove child
runtime identity. Tool paths are public diagnostic metadata and must not contain
secrets, as already required by the manifest guide.

## Alternatives considered

- **Documentation alone:** helps configure overrides but cannot explain a daemon's
  actual selection or preserve launch facts.
- **Re-resolve on inspection:** avoids recording metadata but can misattribute an
  old launch after configuration or environment changes.
- **Version/preflight recipes:** expand execution and configuration contracts
  beyond the approved diagnostic outcome.

## Revisit when

Users need verified child runtime identity or version enforcement, or selection
metadata starts influencing execution or cleanup authority. Reassess scope before
adding either contract.

## References

- [Planner and tool lookup](../../internal/plan/plan.go), [configuration composition](../../internal/plan/configuration.go)
- [Snapshots](../../internal/plan/snapshot.go), [snapshot replacement and acceptance](../../internal/daemon/runtime_state.go)
- [Scoped doctor](../../internal/daemon/doctor.go), [shared plan/error contracts](../../api/v1/plan.go)
- [Private configuration rationale](20261021T001732173Z-register-private-project-configuration.md)
