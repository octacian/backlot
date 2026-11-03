---
title: 'Add project-scoped shared-service allocations'
date: '2026-10-19'
status: superseded
implementation: implemented
summary: 'Share exact project-owned MariaDB servers while retaining private instance allocations and bounded explicit cleanup authority.'
tags: [shared-services, identity, sql, security, lifecycle, cleanup, migration]
supersedes: []
superseded_by: [/docs/adr/20261103T174043965Z-reassociate-retained-projects-through-explicit-current-bindings.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/43"]
implemented_by: ["https://github.com/octacian/backlot/commit/3f52ab439d075533c3c0e5c3ba3389de5237821b"]
---

# Add project-scoped shared-service allocations

## Context and Problem Statement

Worktrees need independent SQL data without one server per scene. Sharing changes
project identity, privilege, interrupted-effect recovery and destructive cleanup;
ordinary container initialization cannot provide those guarantees. Operators also
need bounded explicit recovery when recorded-target proof has drifted.

## Considered Options

- Project-scoped servers with private per-instance allocations.
- Per-instance containers: stronger isolation, but retain server overhead.
- Hooks or name-only adoption: cannot authenticate interrupted effects.
- Machine-wide pooling or automatic relinking: cross unsupported identity boundaries.
- Permanent refusal or forgetting records: traps resources or falsely settles effects.

<a id="decision"></a>

## Decision Outcome

Separate project-owned server resources from instance allocations. Git sharing
uses common-directory filesystem identity plus project location/ID; clones do not
share. Non-Git sharing requires the same canonical directory identity. Moves,
replacements and effective server configuration drift fail without peer takeover.
Pin local images and exact Engine configuration; never implicitly pull or build.

Backlot owns identity, intent, locking and lifecycle. A focused MariaDB manager
creates an empty database and restricted credentials per instance, verifies grant
enforcement and ownership/authentication witnesses, and reclaims only verified
effects. Projects own schema, fixtures and initialization. Administration never
becomes an application/display reference; ordinary records remain redacted.

Persist intent and credentials before effects. Stop/start and attached cancellation
retain allocations; reset/destroy release them only after consumers join. Service
destroy refuses every retained allocation. Recovery never resumes workloads.
Shared state follows the cumulative migration policy rather than a second upgrade
framework. Native residual-process and abandoned-data fences remain applicable.

Explicit invocation-scoped force selects readable recorded targets, never arbitrary
SQL/provider names. It may waive SQL proof after exact server/topology verification,
but preserves peer/consumer, native and filesystem authority. Verify recorded
objects absent before settling; acknowledgement gaps retain ordinary intent.
Instance release can accept missing storage only when both exact server and backing
volume are authoritatively absent. Service force still refuses allocations and
cannot adopt unacknowledged effects or force-remove Docker volumes/networks.

The bounded language-neutral manager protocol separates ownership/effect results
from process exit. It supports allocate, inspect/reconcile and release; force is
release-only. Protocol fields, error framing and procedures belong in the
[maintained contract](/docs/containers.md#project-scoped-shared-services).

### Consequences

Worktrees share overhead and server failure/performance limits rather than
container-level isolation. Engine-specific verification and uncertain recovery
remain maintenance duties. Explicit force accepts recorded-target replacement
risk, not broader deletion authority. Missing/unreadable state requires deliberate
operator maintenance, never guessed identities, broad prune or state reconstruction.

## More Information

[Force maintenance](/docs/containers.md#explicit-operator-force-cleanup),
[migration policy](/docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).
