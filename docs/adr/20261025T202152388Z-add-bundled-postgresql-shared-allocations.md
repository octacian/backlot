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

Add `manager: postgresql` to the existing project-scoped shared-service path.
Reuse its identity, journals, locks, consumer ordering and transient force contract;
keep MariaDB behavior unchanged. PostgreSQL needs explicit PUBLIC restrictions and
an ownership witness that survives nontransactional database creation. This is an
additive decision; custom-command execution remains issue 43 PR 3.

## Context

[Issue 43](https://github.com/octacian/backlot/issues/43) requires empty independent
databases and credentials across worktrees without one server per allocation.
Application schema jobs need ordinary DDL/DML, not database ownership or server
administration. PostgreSQL grants PUBLIC database connections by default, and
`CREATE DATABASE` cannot share a transaction with a later ownership comment.
Name-only recovery or a create-then-comment database would leave an unsafe gap.

## Decision

### Server and application boundary

Reuse the shared lifecycle with an explicit image/arguments, `storage: volume`,
`admin: generated`, port 5432 and bounded readiness. Each service owns one
container, volume and network, a private generated `postgres` password and a
pinned local image ID. Require the image entrypoint contract, `POSTGRES_DB=postgres`,
`PGDATA=/var/lib/postgresql/data` on the sole volume mount and host SCRAM
initialization. Verify the retained topology, command, restart policy and current
loopback binding before SQL, including force. Native/container connection values
follow the existing runtime contract.

Readiness requires authenticated SQL, PostgreSQL identity, SCRAM storage/host
rules and live wrong-password rejection. Unsupported or unverifiable security and
layout preserve state. Engine/image administration remains trusted; shared SQL
isolates application data, not resource usage or visible catalog metadata.
PostgreSQL 17 is the tested target, without a hard major-version gate, automatic
upgrade or compatibility claim for other versions.

Use lib/pq through `pq.NewConnectorConfig`, validating explicit connection fields
at Backlot's boundary. Connect only to the verified TCP endpoint with retained
credentials and the existing private local transport without TLS; ignore ambient
PG settings without environment mutation or generated files. Reject selected
PostgreSQL work if protocol debugging was enabled at process
startup; unselected services remain unaffected. Every privileged physical
connection and reconnect uses `search_path=pg_catalog,pg_temp` before queries,
excluding application-writable schemas while preserving application schema behavior.
Reuse the manager contract and safe-error boundary; keep driver configuration and
validation details in the [adapter](../../internal/manager/postgresql.go).

Contexts and PostgreSQL's separate cancellation connection cannot alone bound an
unresponsive established socket. Use the driver's supported Dialer hook for finite
synchronous socket operations, preserving the earlier startup deadline, bounding
cancel/close and retaining healthy cancellation. The response deadline is renewed
on writes, never reads; do not abandon driver calls in background goroutines.
Combined completion must fit existing owner/shutdown budgets, rather than treating
a per-socket timeout as an end-to-end guarantee. The
[transport adapter](../../internal/manager/postgresql_transport.go) and
[boundary regressions](../../internal/daemon/shared_postgresql_boundary_test.go)
own the timeout mechanics and stalled-connection/privileged-resolution proofs.

Each allocation has one database, one application login role and one NOLOGIN
ownership role. Derive the owner role from the recorded database name and use the
recorded username for the application role; reject equal names and retained sibling
collisions. The owner has no password, memberships or administrative attributes;
never grant membership in it. These identities use existing private intent.

Grant the application CONNECT/TEMPORARY/CREATE only on its database and USAGE/CREATE
on admin-owned `public`, without grant option. This owner-approved contract permits
named schemas and trusted extensions/publications, without database ownership,
CREATEDB/CREATEROLE, superuser, replication, BYPASSRLS, role memberships, privileged
extensions or peer/server administration. Projects own application objects and
default privileges; do not rewrite their grants or add a permission mode, schema
provisioner or cluster-wide event triggers. Privileged applications can use private
containers or explicit administration.

Revoke PUBLIC database privileges on `postgres`, `template1` and allocations;
`template0` stays nonconnectable. Revoke PUBLIC privileges on allocation `public`
schemas before exposure. Database connection denial supplies the peer boundary;
PUBLIC function/type/language defaults do not grant entry. Verify actual ACLs,
role attributes and ownership on reuse; preserve drift rather than repair it.

Bootstrap maintenance REVOKEs and a domain-separated public fingerprint of the
existing server capability in `COMMENT ON DATABASE postgres` atomically, under the
service lock. Only the exact topology/security-verified server with supported
pristine catalogs and no retained allocation facts/effects may initialize. The
default maintenance comment identifies pre-bootstrap state; a missing/replaced
witness with restricted ACLs or allocation effects is drift. A matching witness
still requires actual ACL/security checks. This SQL metadata shares the volume
lifetime, with no new registry, reset command or state field. Force can skip
maintenance proof only for recorded-allocation release; it cannot bootstrap,
repair or rewrite the marker.

### Interrupted effects and release

Persist intent and credentials before effects under existing locks. Bind ownership
at each SQL effect boundary:

1. Create both roles and domain-separated ownership fingerprints transactionally,
   initially NOLOGIN with NULL passwords. Comments are public evidence, never
   capabilities; applications cannot change them.
2. Create from `template0` with the verified NOLOGIN OWNER and CONNECTION LIMIT 0
   in the same statement. Atomic ownership identifies the nontransactional database
   effect; the zero limit closes the PUBLIC-default connection gap.
3. Transactionally apply grants, install the retained password with SCRAM storage,
   enable application LOGIN and lift the connection limit. Verify ownership,
   security and live retained-password authentication before ready or ready reuse.

Partial recovery requires matching fingerprints, exact role attributes/memberships
and the database owner relationship. Initial NOLOGIN/NULL-password states cannot
supply live login proof; a valid partial allocation may resume from catalog facts.
Finalization rollback retains that initial state. Committed but unacknowledged
finalization must pass ready checks without reinstalling its password. Preserve
unexpected LOGIN/passwords, privileges, memberships, owners or markers; do not
normalize drift, adopt by name or recreate a missing acknowledged ready allocation.
Use existing fault checkpoints at real commit/effect boundaries, without another
persisted stage or verifier protocol.

After consumer joins, verify every existing target before deleting any. A finalized
database requires live retained-password authentication for default release;
initial partial targets use their witness proof. Drop the exact database first,
then both roles transactionally, and verify all three absent before acknowledging
release. Surviving role witnesses cover a lost database-drop acknowledgement.
Under release intent with the database absent, verify remaining role fingerprints,
attributes/memberships and allowed initial/finalized shape; no login to an absent
database or maintenance CONNECT is required. This proves role identity, not
post-drop password equality. Drift still preserves roles. Repeated release observes
absence without inventing replacement identity.

Sessions, prepared transactions, unexpected role dependencies and unavailable
verification retain cleanup obligations. Never terminate sessions, use PostgreSQL
`WITH (FORCE)`, `DROP OWNED`, `REASSIGN OWNED` or cascading peer cleanup. Explicit
instance destroy force may waive SQL fingerprints, authentication/grants and owner
mismatch only for recorded names, after existing topology, peer and consumer fences.
It cannot waive role dependencies or discover deletion targets from owner scans.
Keep force invocation-only and release-only, with existing exact-server-and-volume
absence reconciliation, retained-allocation fences and no replacement infrastructure.

### Retention

Existing shared records represent the declaration and derived role identity;
PostgreSQL adds no persisted fields or migration. Preserve MariaDB interpretation
and matching-binary guidance; old binaries do not understand PostgreSQL records. Apply the deferred
`saveShared` Server/Network/Volume validation before saving provider responses;
rejection preserves prior valid intent/capability and reopenable state.

Add no background owners, retry loops, timer owners or unused-provider work.
Socket deadlines belong to existing connections. Offline selection, secrets/startup
consent, lifecycle policies and cleanup authority retain the shared contract.

## Consequences

One extra role per allocation and a maintenance fingerprint make interrupted
creation/release recoverable without application database ownership or another
persistent protocol. The maintenance burden is engine-specific ACL/authentication
checks, SQL recovery states and one Go driver with a small transport adapter.
Driver upgrades must recheck explicit configuration, debug isolation, privileged
resolution and startup/request/close coupling. Default cleanup may require manual
restoration; explicit force retains recorded-target replacement risk. Runtime and
platform acceptance remain separate from this architectural decision.

## Alternatives considered

Application-owned databases save one role but grant unnecessary authority;
database-local CREATE supplies the chosen schema/extension permissions. An
admin-owned database followed by a comment leaves the create gap unauthenticated.
A new ownership registry still needs to bind that nontransactional effect and
adds storage coordination. One database with per-allocation schemas changes the
promised isolation boundary. Per-instance containers remain suitable for privileged
applications but do not meet the shared-server overhead goal.

A `psql` subprocess adds a host executable dependency and less direct cancellation/
error handling. The tested pgx v5.11.0 `service=''` workaround still reads service
files. lib/pq's explicit-config path supplies SCRAM without ambient parsing or
extra module dependencies; generated service files, a fork or a subprocess add
unnecessary lifecycle scope. Bun's pgdriver brings ORM-related dependencies.
A separate manager service, plugin framework or migration engine adds no required
behavior.

## Revisit when

A project needs database-owner/privileged-extension permissions, different image/
storage/authentication support, another version has actual evidence, or atomic
witnesses cannot preserve bounded recovery. Reopen scope before adding custom
execution, cross-daemon coordination, session termination or persisted fields.

## References

- [Issue 43](https://github.com/octacian/backlot/issues/43)
- [Shared allocation/lifecycle decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
- [Retained migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Shared API and dependency policy](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Shared runtime and force contract](../containers.md#project-scoped-shared-services)
- [Shared lifecycle](../../internal/daemon/shared.go) and [manager analogue](../../internal/manager/mariadb.go)
- [PostgreSQL CREATE DATABASE](https://www.postgresql.org/docs/17/sql-createdatabase.html)
- [PostgreSQL privileges](https://www.postgresql.org/docs/17/ddl-priv.html), [role comments](https://www.postgresql.org/docs/17/sql-comment.html) and [DROP DATABASE](https://www.postgresql.org/docs/17/sql-dropdatabase.html)
- [lib/pq explicit configuration](https://github.com/lib/pq/blob/v1.12.3/connector.go), [release](https://github.com/lib/pq/releases/tag/v1.12.3) and [SCRAM implementation](https://github.com/lib/pq/blob/v1.12.3/conn.go)
