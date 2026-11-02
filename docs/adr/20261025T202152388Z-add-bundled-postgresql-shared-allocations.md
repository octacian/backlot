---
title: 'Add bundled PostgreSQL shared allocations'
date: '2026-10-25'
status: accepted
implementation: implemented
summary: 'Add a focused PostgreSQL manager with restricted application roles and atomic ownership witnesses while reusing the shared lifecycle.'
tags: [shared-services, postgresql, sql, security, lifecycle]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/43"]
implemented_by: ["https://github.com/octacian/backlot/commit/90df9aa1c106029def05c18223648e589b6b70de"]
---

# Add bundled PostgreSQL shared allocations

## Context and Problem Statement

PostgreSQL allocations need application schema DDL/DML without database ownership
or peer/server administration. PUBLIC access defaults and nontransactional database
creation create security and interrupted-effect gaps beyond ordinary shared SQL
lifecycle coordination.

## Considered Options

- Restricted application roles plus separate NOLOGIN owners and atomic witnesses.
- Application-owned databases: save a role but grant unnecessary authority.
- An ownership comment after database creation: leaves the creation gap unauthenticated.
- One database with per-allocation schemas: changes the promised isolation boundary.
- A psql subprocess or other drivers: add host/dependency scope without establishing
  the required explicit configuration and cancellation boundary.

## Decision Outcome

Add a focused PostgreSQL manager to the existing shared lifecycle. Keep pinned
local images, exact topology, private administration and authenticated readiness,
including wrong-password rejection. PostgreSQL 17 is the tested target, not an
automatic version-upgrade policy. Projects own application objects and grants.

Give each allocation one database, a restricted application role and a distinct
NOLOGIN owner role. Applications receive only required in-database capabilities,
without owner membership, server administration or grant options. Revoke default
PUBLIC access. Initialize maintenance restrictions and a server-capability witness
atomically only on verified pristine state; markers never replace security checks.

Persist intent/credentials before effects. Create initial NOLOGIN roles with
ownership fingerprints transactionally, then create the database with its verified
owner and connection limit zero in the same statement. Finalize grants/password/login
and open connections transactionally. This binds ownership to nontransactional
creation without exposing the PUBLIC-default connection interval. Verify ownership,
ACLs and retained-password authentication before ready reuse; never repair/adopt drift.

Use lib/pq explicit configuration isolated from ambient PG settings. Privileged
connections exclude application-writable schemas, including reconnects; reject
protocol debugging and bound established-socket I/O within cancellation/join budgets.

After consumer joins, verify targets, drop database before roles and prove all
absent. Preserve active sessions, dependencies and uncertain proof rather than
terminating/cascading. Explicit force waives SQL proof only for recorded targets,
retaining exact topology, peer/consumer and dependency fences; it never repairs
maintenance bootstrap or expands deletion scope.

### Consequences

An extra role and SQL witnesses make partial creation/release recoverable without
application database ownership or another registry. Driver/ACL/transport verification
is ongoing work. Administration remains trusted, catalogs visible and performance/
failure shared; privileged applications may still need per-instance containers.

## More Information

[Shared foundation](/docs/adr/20261019T050443877Z-add-project-scoped-shared-service-allocations.md),
[manager](/internal/manager/postgresql.go), [transport boundary](/internal/manager/postgresql_transport.go).
