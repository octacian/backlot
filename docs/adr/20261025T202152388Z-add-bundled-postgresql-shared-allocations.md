---
title: 'Add bundled PostgreSQL shared allocations'
date: '2026-10-25'
status: accepted
summary: 'Add a focused PostgreSQL manager with restricted application roles and atomic ownership witnesses while reusing the shared lifecycle.'
tags: [shared-services, postgresql, sql, security, lifecycle]
supersedes: []
superseded_by: []
---

# Add bundled PostgreSQL shared allocations

Add `manager: postgresql` to the existing project-scoped shared lifecycle. One
Docker server serves independent instance databases and restricted application
roles. Reuse the [foundation decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
for identity, retained intent, locking, consumer ordering and bounded force.
MariaDB behavior is unchanged; custom-command execution remains issue 43 PR 3.

## Context

[Issue 43](https://github.com/octacian/backlot/issues/43) requires independent
worktree data without a server per allocation. PostgreSQL needs additional
privilege and recovery decisions: PUBLIC can connect to databases by default,
and `CREATE DATABASE` cannot be committed atomically with a later ownership comment.
Applications need schema DDL/DML, without database ownership or server administration.

## Decision

Use the existing container/volume/network path with generated private administration,
a pinned local image and verified topology. Readiness requires authenticated SQL,
PostgreSQL identity and enforced SCRAM authentication, including live wrong-password
rejection. Preserve unsupported or unverifiable configurations. PostgreSQL 17 is the
tested target; there is no hard major-version gate or automatic upgrade. The
[runtime guide](../containers.md#project-scoped-shared-services) owns image/layout
requirements and native/container connection instructions.

Each allocation has one database, one restricted application role and a separate
NOLOGIN owner role derived from the recorded database name. Neither role has
administrative privileges or memberships, and applications cannot join the owner
role. Grant application CONNECT/TEMPORARY/CREATE within its database and USAGE/CREATE
on admin-owned `public`, without grant option. This permits named schemas and trusted
extensions/publications without database ownership or peer/server administration.
Projects own their application objects and grants; no permission mode or schema
provisioning subsystem is added.

Revoke PUBLIC database access to maintenance and allocation databases, and PUBLIC
privileges on allocation `public` schemas. Initialize maintenance restrictions and
a public fingerprint of the retained server capability atomically, only on a
verified pristine server with no allocation facts/effects. This volume-lived SQL
witness distinguishes interrupted bootstrap from drift without another registry.
A matching marker does not replace ACL/security checks; drift preserves state and
cannot be repaired by force.

Use lib/pq's explicit configuration path to exclude ambient PG settings without
environment mutation or generated configuration. Reject startup-enabled protocol
debugging. Every privileged connection/reconnect excludes application-writable
schemas from name resolution. Operations must fit existing cancellation/join budgets,
including an unresponsive established socket; query contexts alone are insufficient.
Use the supported Dialer hook to bound synchronous I/O, without abandoning driver
calls in background goroutines. Connector fields, deadlines and regression scenarios
belong in the [manager](../../internal/manager/postgresql.go),
[transport](../../internal/manager/postgresql_transport.go) and
[boundary tests](../../internal/daemon/shared_postgresql_boundary_test.go).

### Ownership and interrupted effects

Persist intent and credentials before SQL effects. Establish ownership at the
actual effect boundaries:

1. Create both roles and domain-separated ownership fingerprints transactionally,
   initially NOLOGIN with NULL passwords. Fingerprints are public evidence, never
   capabilities, and applications cannot modify them.
2. Create the database from `template0` with its verified NOLOGIN owner and connection
   limit zero in the same statement. Ownership is atomic with the nontransactional
   effect; the zero limit closes the PUBLIC-default connection gap.
3. Transactionally apply grants, install the retained SCRAM password, enable
   application login and open connections. Verify ownership, security and live
   retained-password authentication before ready or ready reuse.

Partial recovery uses the fingerprints, role attributes/memberships and database
owner relationship; initial NOLOGIN roles cannot supply live authentication proof.
Committed finalization must pass ready checks without resetting its password.
Preserve mismatches and missing acknowledged ready allocations rather than repairing,
recreating or adopting by name. Detailed catalog checks and checkpoints remain in
the implementation and recovery tests.

After consumer joins, verify targets before default release, including retained
credentials while a finalized database exists. Drop the database before both roles
and verify all three absent. Surviving role witnesses permit interrupted release
after database absence without claiming password equality. Unavailable verification,
active sessions or unexpected dependencies retain cleanup obligations; never terminate
sessions or cascade deletion. Explicit force may waive SQL ownership/authentication
proof only for recorded targets, retaining topology, peer/consumer and dependency
fences. It never bootstraps maintenance, expands targets or persists authority.

Reuse existing shared records and lifecycle policies without new persisted fields,
migrations, background owners, retry loops or unused-provider work. Retain matching-
binary guidance. Validate provider handles before saving; rejection must preserve
prior valid intent/capability and reopenable state.

## Consequences

The additional owner role and maintenance witness provide recoverable creation and
release without application database ownership or another persistent protocol.
The cost is PostgreSQL-specific ACL/authentication and recovery checks, plus one
driver and its transport adapter. Driver upgrades must reverify ambient isolation,
privileged resolution and bounded cancellation/close behavior. Uncertainty can require
manual restoration; explicit force retains recorded-target replacement risk.
Engine administration is trusted, catalogs remain visible, and allocations share
server performance/failure limits rather than container-level isolation.

## Alternatives considered

- Application-owned databases save a role but grant unnecessary authority. An
  admin-owned database followed by a comment leaves the create gap unauthenticated;
  a separate ownership registry adds coordination without solving that gap.
- One database with per-allocation schemas changes the promised allocation boundary.
  Per-instance containers remain appropriate for privileged applications, but do not
  meet the shared-server overhead goal.
- A `psql` subprocess adds a host dependency and less direct cancellation/error
  handling. The evaluated pgx service-string path did not establish ambient isolation;
  Bun's pgdriver adds ORM-related dependencies. lib/pq's explicit configuration
  supplies the required isolation and SCRAM without those additions.

## Revisit when

Projects need database-owner/privileged-extension permissions, different image or
storage/authentication support, another version has actual evidence, or ownership
witnesses cannot preserve bounded recovery. Cross-daemon sharing, session termination
and new persisted state require a scope decision.

## References

- [Issue 43](https://github.com/octacian/backlot/issues/43)
- [Retained migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Shared API and dependency policy](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [PostgreSQL CREATE DATABASE](https://www.postgresql.org/docs/17/sql-createdatabase.html), [privileges](https://www.postgresql.org/docs/17/ddl-priv.html) and [DROP DATABASE](https://www.postgresql.org/docs/17/sql-dropdatabase.html)
- [lib/pq explicit configuration](https://github.com/lib/pq/blob/v1.12.3/connector.go)
