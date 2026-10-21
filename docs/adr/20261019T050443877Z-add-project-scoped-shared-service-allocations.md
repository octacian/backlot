---
title: 'Add project-scoped shared-service allocations'
date: '2026-10-19'
status: superseded
summary: 'Separate retained project-owned servers from instance allocations with explicit ownership observations, private provisioning and exact configuration conflicts.'
tags: [shared-services, identity, sql, security, lifecycle]
supersedes: []
superseded_by: [20261020T233406000Z-allow-explicit-force-cleanup-of-recorded-shared-targets.md]
---

# Add project-scoped shared-service allocations

Extend the generic private-container runtime with separately owned project shared
services and per-instance allocations. Backlot owns durable identity, intent,
credentials, serialization and lifecycle ordering; a focused manager provisions,
verifies and reclaims engine-specific effects. Existing private containers remain
available. This is the accepted #43 direction after Gate 1 and concrete Gate 2 scope review;
implementation and evidence status live in the implementation plan.

## Context

Separate worktrees need independent application data without running an entire
SQL server per scene. Checkout identity and the persistent instance key include
checkout/scene, so neither can identify the shared project. SQL provisioning was
previously outside product scope; sharing introduces explicit privilege, ownership
and recovery obligations that generic image initialization cannot satisfy.

## Decision

Identify a Git shared project by common-directory filesystem identity, project
location relative to the checkout, and declared project ID. Canonical common paths
are retained too: moved/replaced anchors fail conservatively without automatic
relinking. Independent clones do not share. Non-Git projects share only the same
canonical directory and filesystem identity. Branch names and Git HEAD are not
ownership. The daemon's private state remains the local administrative boundary;
separate state directories do not coordinate a machine-wide pool.

Retain exact server declarations and local endpoint in a configuration digest.
A conflicting branch cannot stop, replace or upgrade a peer server. New incompatible
servers require distinct declarations. Pin the first selected local image ID;
never pull/build implicitly or infer version compatibility. Own a separate server,
volume and network through recorded capability labels, independently from scene
containers and allocation generations. Native consumers use an owned loopback
mapping; container consumers explicitly attached to the allocation use the shared
network alias and internal port. Map every connection value through existing
explicit environment references. Administration never becomes an application value.

The Gate 1 MariaDB declaration explicitly selects an image, arguments, volume
storage, generated private administration, port 3306 and bounded readiness.
Allocate an empty database plus restricted user/password per instance resource.
Generate grant identifiers without wildcard characters. Verify atomic database
ownership comments using a one-way ownership fingerprint and user authentication
hashes before adopting or reclaiming interrupted effects. Unexpected grants,
identity mismatch or unavailable verification preserve the resources. Application
schema/migrations/fixtures remain project jobs; shared servers are a common
performance/failure boundary, not container-level isolation.

Private state version 7 fences durable destructive progress from older binaries. Reject older and newer unsupported formats
unchanged; matching old binaries must clean their resources before a fresh state
directory is selected. Record server/allocation intent and credentials before
effects. Release only after consumers stop/join. Stop/start retains allocations;
reset/destroy releases only the target. Idle shared services remain until explicit
stop; service destruction refuses all retained allocations, including stopped ones.
Shutdown/recovery joins consumers before stopping shared services, preserves data
and requires explicit execution restart. Unknown effects retain cleanup obligations.

Use a language-neutral command protocol: one bounded structured JSON request on
stdin and one validated JSON result on stdout for allocate, inspect/reconcile and
release. Protocol results distinguish verified ownership, absence and uncertainty,
and ready, partial and released effects; exit status cannot replace these facts.
Backlot owns IDs, intent/credentials where applicable, cancellation, deadlines,
ordering and truthful outcomes. Project commands are trusted cooperative code;
unavailable or unverifiable cleanup code preserves resources and obligations.
Gate 1 defines/validates the initial messages using MariaDB; custom command
execution, PostgreSQL and the non-database fixture are subsequent user gates and
may refine this small contract. There is no plugin framework or required language.

## Consequences

Worktrees share server overhead while retaining separate data and application
credentials. SQL managers introduce engine-specific responsibility and require
real permission/isolation evidence. Configuration conflicts and uncertain ownership
can require explicit maintenance rather than automatic repair. Separate daemon
state roots and external servers are never automatically combined or adopted.

## Alternatives considered

Per-instance generic containers remain appropriate for privileged workloads and
stronger separation. Machine-wide automatic pooling merges unrelated configuration
and identity boundaries. Setup/teardown hooks alone cannot verify interrupted
multi-step effects. A broad plugin framework before concrete engine/non-database
evidence adds unsupported contracts. Name-only adoption or automatic relinking
can reclaim foreign resources and is rejected.

## Revisit when

Use PostgreSQL 17 and a non-Go non-database command fixture to refine the minimal
protocol. Expand storage/administrative configuration only with concrete needs and
review. Revisit sharing across daemon roots only with an explicit machine-wide
coordination design; never infer sharing from matching images or project names.

## References

- [Issue 43](https://github.com/octacian/backlot/issues/43)
- [Product requirements](../prd.md#project-scoped-shared-services)
- [Runtime contract](../containers.md#project-scoped-shared-services-gate-1)
- [Implementation status](../implementation-plan.md#shared-services-issue-43)
- [Existing lifecycle decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Offline contracts](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
- [MariaDB database comments](https://mariadb.com/docs/server/reference/sql-statements/data-definition/create/create-database)
- [MariaDB user authentication](https://mariadb.com/docs/server/reference/sql-statements/account-management-sql-statements/create-user)
