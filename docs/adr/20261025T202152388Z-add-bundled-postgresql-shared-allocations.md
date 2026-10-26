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

Retain the existing explicit image/arguments, `storage: volume`, `admin: generated`
and readiness deadline; require port 5432 for PostgreSQL (3306 for MariaDB).
Each declared PostgreSQL service owns one container, volume and bridge network,
a generated private `postgres` administration password and the pinned local image
ID. Use the image's PostgreSQL entrypoint contract, `POSTGRES_DB=postgres`, explicit
`PGDATA=/var/lib/postgresql/data` on the sole volume mount, and host SCRAM
initialization. Retain and verify these settings, effective command, restart policy,
image, mounts, network and current loopback binding before SQL, including force.
Native consumers use the current dynamic loopback port; attached containers use the
existing shared alias and 5432. No extra container, sidecar, gateway or daemon.

Require PostgreSQL identity, working generated administration and password
enforcement: SCRAM password storage and host authentication rules, no uninspectable
or permissive host rules, and live rejection of wrong administrative credentials.
Readiness includes bounded authenticated SQL and security verification, not an open
TCP port. Trusted Engine/image administration remains outside application isolation;
file-rule inspection alone is not proof of loaded authentication. Exercise live
negative authentication from native and bridge paths. Preserve on unsupported
security/layout rather than infer compatibility. Exercise PostgreSQL 17 only;
there is no hard major-version gate, automatic upgrade or claim for other versions.

Use `github.com/lib/pq` v1.12.3 through `database/sql.OpenDB` and
`pq.NewConnectorConfig` with a fresh `pq.Config` literal. Supply the verified
single TCP host/port, database, user, nonempty retained password, disabled TLS for
the existing private local transport, UTF8/ISO settings and explicit operation
contexts. Leave service, password-file, alternate hosts, options and runtime values
unset except a fixed privileged startup `search_path=pg_catalog,pg_temp`. Apply that
setting to every physical `postgres` management connection and reconnect before
queries, including connections into application databases; exclude all app-writable
schemas from privileged function and relation resolution. Keep the administrator's
temporary namespace last, and retain ordinary application-role schema behavior and
the agreed CREATE/trusted-extension permissions. Never call the environment/DSN
constructors. This supported path bypasses
ambient PG service/password/TLS/fallback/runtime settings without process-global
environment mutation or generated files. Validate explicit fields at Backlot's
boundary: this constructor does not apply the DSN parser's validation/defaults.
Capture whether `PQGO_DEBUG=1` was enabled at process startup and reject selected
PostgreSQL work before any driver use when enabled: the driver captures that setting
at initialization and can print private protocol/configuration. Unselected services
remain unaffected. Do not enable protocol tracing. Update dependency notices when implementation adds it.
Reuse the manager request/result contract and safe-error boundary; do not implement
PostgreSQL wire authentication, a custom manager framework or a parallel lifecycle.

Bound the original established socket through the supported connector Dialer hook;
a query context or separate CancelRequest alone cannot interrupt a stalled server.
Use a private synchronous connection adapter with a five-second absolute response/
write deadline renewed on each outgoing request write, never on reads. Preserve an
earlier complete two-second startup deadline across authentication writes; clearing
that startup deadline must retain a finite socket boundary. Renew after idle periods
and bound Terminate/Close and the separate cancel connection as well. Keep contexts
and healthy server-assisted cancellation; do not weaken its existing acceptance.
Prepared exchanges and Close can add bounded stages, so five seconds is not an
end-to-end operation/join claim. Demonstrate combined completion within existing
owner/shutdown budgets on an already authenticated unresponsive connection, without
restoring responsiveness to unblock it; verify owner/lock release and preserve SQL
intent and uncertain effects. Do not abandon a blocked driver call in a goroutine.
Test actual privileged reuse/default release with an app-defined overload too;
configuration inspection or responsive `pg_sleep` cancellation is not that proof.

Each allocation has exactly one database, one login application role and one
NOLOGIN ownership role. Derive the ownership role name from the recorded database
name (the same spelling in PostgreSQL's separate role namespace); the application
role uses the recorded username. Reject equal names and cross-role collisions with
retained siblings. These names and the ownership token already exist in private
intent; no additional journal or persisted identifier is needed.

The ownership role owns the database and has no memberships or administrative
attributes. Never grant membership in it. The application role has no superuser,
CREATEDB, CREATEROLE, replication, BYPASSRLS or role memberships. Grant CONNECT,
TEMPORARY and CREATE on only its database, and USAGE/CREATE on the admin-owned
`public` schema, without grant option. This single owner-approved permission
contract permits app-owned additional schemas and their tables, sequences, indexes
and functions, temporary tables, and PostgreSQL's database-local CREATE authority
including trusted extensions/publications. It grants neither database ownership,
CREATEDB/CREATEROLE, ownership-role membership, privileged extensions nor peer/server
administration. Projects needing those privileges use private containers or explicit
administration; do not add a permission mode or schema provisioning subsystem.

Revoke PUBLIC database privileges on owned maintenance databases `postgres` and
`template1`, and every allocation; `template0` remains nonconnectable. Revoke PUBLIC
privileges on each allocation's `public` schema before exposing it. Verify these
boundaries on reuse; preserve unexpected grants or privileged roles rather than
repairing them. Bootstrap the maintenance REVOKEs and a domain-separated public
fingerprint of the existing server capability in `COMMENT ON DATABASE postgres`
in one transaction. This is persistent SQL metadata sharing the owned server's
volume lifetime, not another registry or Go state field. Under the existing service
lock, only the exact topology/security-verified server with the supported pristine
default PostgreSQL catalogs and no retained allocation facts/effects may initialize.
The default PostgreSQL maintenance comment is the supported pre-bootstrap value;
a missing/replaced witness with restricted ACLs or allocation effects is drift.
A matching marker still requires actual ACL, ownership and security verification;
mismatch or nonpristine state preserves. Force may skip maintenance proof only for
bounded recorded-allocation release; it never initializes/repairs maintenance,
rewrites the witness or expands deletion targets. No marker-reset command or new
state field/version/owner is introduced. PUBLIC function EXECUTE and type/language USAGE defaults are not a
peer connection grant: peers cannot enter the database. Projects control their
own objects/default privileges; do not install cluster-wide event triggers or
rewrite application grants. Catalog names/comments remain visible metadata; shared
SQL is not tenant-grade containment or resource isolation.

### Interrupted effects and release

Persist ordinary intent and credentials first, under existing instance/service
locks. Create both roles and their domain-separated one-way ownership fingerprints
in a single SQL transaction, initially with application NOLOGIN and PASSWORD NULL.
The ownership role is always NOLOGIN with a NULL password. Role comments are public
fingerprints, never capabilities; unprivileged applications cannot change them.
Inspect fingerprints, exact role attributes/memberships and the database's atomic
owner relationship before adopting effects, not names or successful login alone.

For unacknowledged initial effects, both before database creation and with the
zero-limit database present, role fingerprints plus the NOLOGIN/NULL-password
invariants establish ownership; live application login is impossible and is not
required. A mismatched marker, unexpected LOGIN/password, privilege/membership or
owner is drift: preserve without creating/finalizing/deleting or resetting secrets.
A valid partial allocation may resume from these catalog facts using retained
intent; no new persisted stage or password verifier protocol is needed.

Create the database from `template0` with OWNER set to the verified NOLOGIN role
and CONNECTION LIMIT 0 in the same CREATE statement. The owner relationship is
atomic with the nontransactional database effect; existing peers cannot connect
during the PUBLIC-default gap. As administrator in the new database, transactionally
apply database/schema grants, install the retained application password using
server SCRAM storage, enable application LOGIN and lift the connection limit.
Verify ownership, security and live retained-password authentication to that
allocation before ready or ready reuse. A finalization rollback retains the initial
NOLOGIN/NULL-password state; a committed but unacknowledged finalization must pass
ready-state checks, not rerun password installation. Changed ready credentials
preserve the allocation; never overwrite them to make authentication succeed.
An interrupted transaction rolls back; an unacknowledged database is recognizable
through its authenticated owner. Reconcile only the exact recorded partial targets;
never recreate an acknowledged ready allocation that disappeared, normalize drift,
or adopt a similarly named database with a different owner. Keep SQL checkpoints
at these real commit/effect boundaries for existing fault machinery.

After consumer joins, verify all existing targets before deleting any. With a
finalized database present, include live retained-password authentication before
default release, so credential drift preserves data. Initial partial targets use
the initial witness proof above. Drop the exact database first, without PostgreSQL `WITH (FORCE)` or session termination,
then drop the application and ownership roles in a transaction; verify all three
absent before acknowledging release. A role witness therefore survives a lost
database-drop acknowledgement. When the database is already absent under release
intent, inspect the remaining role fingerprints, attributes/memberships and allowed
initial or finalized shape; do not require login to a deleted database or grant
maintenance CONNECT. These witnesses establish ownership for role deletion, not
credential equality: changed finalized passwords after database absence do not
prevent releasing those witnessed roles. Marker/privilege/membership drift still
preserves them. Never use database absence to recreate an acknowledged ready
allocation. Existing sessions, prepared transactions, unexpected role dependencies or unavailable verification retain cleanup obligations. Never
use `DROP OWNED`, REASSIGN OWNED or cascading peer cleanup. Repeated release observes
absence; it does not invent replacement identity.

Explicit instance destroy force may waive SQL fingerprints, authentication/grants
and owner mismatch only for these recorded names, after the existing topology,
peer collision and consumer fences. It does not waive other roles' dependencies,
terminate foreign sessions or delete a database discovered from a role owner scan.
Force remains invocation-only, release-only and absent from persisted requests.
Keep existing exact-server-and-volume absence reconciliation and service-destroy
rules, including every retained allocation fence and no replacement infrastructure.

### Retention and delivery

The existing state-v10 records can represent the declaration and derived role
identity; broaden validated manager/port values without changing their layout or
MariaDB interpretation. Do not bump state version or add a migration without an
actual persisted format need. Old binaries do not understand new PostgreSQL
records; retain matching-binary operational guidance. Apply the separately deferred
`saveShared` Server/Network/Volume handle checks before committing provider responses;
a rejected write must leave valid previous intent/capability and reopenable state.

No new background owners, retries, per-allocation timer owners or unused-provider
work; socket deadlines belong only to existing driver connections. Unselected and
offline behavior, secrets/startup consent, persistence/reset/disposable policies,
shutdown/recovery and private/native cleanup authority retain the accepted contract.
Implement using the existing fixtures and fault/ack-gap barriers; demonstrate real
CLI/daemon native/container writes and peer denial before broad regression suites.
Only independently reviewed and accepted architecture authorizes dependent delivery.

## Consequences

One extra NOLOGIN role per allocation makes database creation recoverable without
application database ownership or a new persistent protocol. Engine-specific ACL,
role, authentication and partial-release checks and one Go driver are the cost.
Initial roles carry no password until finalization; after database absence, default
release relies on witnessed role identity without claiming live credential proof.
The driver needs explicit config validation, a startup protocol-debug guard,
trusted privileged resolution and the small supported transport adapter. Recheck
its request-write/startup/close coupling and stalled-connection proof on upgrades.
Default cleanup can require manual restoration; explicit force accepts the existing
recorded-target replacement risk. Shared resource/failure and visible metadata
boundaries remain. PostgreSQL coverage and a runnable example extend existing
fixture machinery; this decision does not establish runtime or platform acceptance.

## Alternatives considered

Application-owned databases save one role but grant unnecessary database ownership;
database-local CREATE supplies the chosen schema/extension permissions without it.
An admin-owned database followed by a comment cannot authenticate the create gap.
One shared database with schemas changes the promised allocation boundary and
complicates peer/default-privilege isolation. A new SQL ownership registry still
needs to bind a nontransactional database effect and adds storage coordination.
Per-instance containers remain the smaller choice for privileged applications.

A psql subprocess adds a daemon-host executable dependency and less direct
cancellation/error handling. pgx supplies maintained SCRAM, but the tested pinned
v5.11.0 `service=''` workaround still reads service files and does not establish
ambient isolation. lib/pq's maintained explicit-config adapter supplies SCRAM and
standard SQL without that parsing or extra module dependencies. A private generated
service file, fork or subprocess would add lifecycle scope without need here.
Bun's pgdriver has explicit options, but brings its ORM-related dependencies where
lib/pq suffices. A separate manager service, generic plugin layer or database migration engine adds no required behavior.

## Revisit when

A project needs database-owner or privileged-extension permissions, a different image/storage
layout or authentication mechanism, another version has actual evidence, or atomic
owner witnesses cannot preserve bounded recovery. Reopen scope before adding custom
execution, cross-daemon coordination, force session termination or persisted fields.

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
