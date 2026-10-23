---
title: 'Add project-scoped shared-service allocations'
date: '2026-10-19'
status: accepted
summary: 'Share exact project-owned MariaDB servers while retaining private instance allocations and bounded explicit cleanup authority.'
tags: [shared-services, identity, sql, security, lifecycle, cleanup, migration]
supersedes: []
superseded_by: []
---

# Add project-scoped shared-service allocations

Separate retained project-owned servers from per-instance allocations so linked
worktrees share server overhead while keeping independent application data and
credentials. Backlot owns identity, durable intent, serialization and lifecycle;
a focused MariaDB manager verifies and reclaims engine-specific effects. Existing
private containers remain available for stronger separation.

Implement this MariaDB slice through existing planning, storage and execution
boundaries. Preserve merged decisions for private project configuration, attached
execution, cooperative native supervision, selected startup disclosure and
cumulative state migration. Implementation and evidence status belong in the
[implementation plan](../implementation-plan.md#shared-services-issue-43).

## Context

[Issue 43](https://github.com/octacian/backlot/issues/43) requires independent
worktree data without a SQL server per scene. Instance identity includes checkout
and scene and cannot identify the sharing project. Sharing introduces privilege,
configuration, partial-effect recovery and destructive-cleanup obligations that
ordinary container initialization does not address.

The owner also requires an explicit cleanup escape for readable recorded targets
whose ownership proof has drifted or whose server/storage has disappeared.
Permanent refusal can otherwise trap allocations and prevent service destruction.
Main now provides the supported v9 migration baseline and settled configuration,
native, attachment and startup-presentation contracts; integration extends them.

## Decision

Identify a Git shared project by common-directory filesystem identity, project
location relative to the checkout and declared project ID. Retain canonical anchor
paths and reject moves/replacements without automatic relinking. Independent clones
do not share. Non-Git projects share only the same canonical directory/filesystem
identity. Branch/HEAD and matching project names are not ownership. Separate private
daemon roots do not coordinate a machine-wide pool. Reuse private configuration
composition and effective-drift checks without enrollment or registry mutation.

Retain exact server declaration and local Docker endpoint in a configuration digest;
conflicting branches cannot replace, stop or upgrade a peer server. Pin the first
selected local image ID; never implicitly pull/build or infer compatibility. Own
server, volume and network with separate recorded capabilities, independently of
instance generations. Native consumers use owned loopback mapping; explicitly
attached containers use the shared network alias/internal port. Select shared
services only through the offline scene dependency closure. Preserve startup
selection metadata and effective configuration provenance alongside that selection.

The bundled manager supports MariaDB 11.4 with explicit image/arguments, retained
volume, generated private administration, port 3306 and bounded readiness. Allocate
an empty database and restricted user/password per instance resource. Generate
grant identifiers without wildcard characters. Verify database ownership comments
using one-way fingerprints and user authentication hashes before adopting or
reclaiming interrupted effects. Unexpected grants, identity mismatch or unavailable
verification preserve resources by default. Projects own schema, fixtures and
fresh-only/each-start initialization. Shared servers remain a common failure and
performance boundary, not container-level isolation.

Connection values use existing explicit application references and sensitivity
propagation. Administration is never an application reference or display value.
Ordinary plans, status, durable results, diagnostics and exports retain redaction.
Explicitly selected persistent startup details may display application credentials
under the settled [startup consent decision](20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md),
using its retained ready-attempt selection and coherent private fixture retrieval.
No additional reveal flag, consent prompt or blanket startup-redaction rule is added.

### Persistence and lifecycle

Use the [cumulative migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).
Advance format 9 to 10 for four new buckets: `shared_projects`, `shared_paths`,
`shared_services`, `shared_allocations`. Retain one private 9-to-10 step in the
existing dispatcher and supplied bbolt write transaction. Validate source-v9
layout/authority before requiring the new buckets; reject any target-namespace
collision. Create four empty buckets without resaving existing records. Preserve
all unrelated key/value bytes, unknown passive data and existing authority checks.
Run final current layout/authority validation and fence in that same transaction;
precommit failure/cancellation rolls back, and a committed fence is one-way.
Fresh state initializes directly at 10. Reject every pre-v9 format, including
experimental shared format 7, unchanged with matching-binary preservation guidance.
Migration performs no provider, process, registry, Git/filesystem or retained-file
effects, provisioning, reset, seeding or reconciliation.

Validate shared authority-bearing journals and their concrete project, service and
instance parent links before recovery, using transaction-local typed decoding.
Retain legitimate interrupted intent/effect states; do not validate historical
records against current files or invent missing authority. Record server/allocation
intent and credentials before effects. Serialize by existing instance ownership
and ordered service locks. Accepted shared mutations have cancellation and joins;
shutdown fences registration, joins mutation/expiry/execution owners, then stops
shared services before releasing storage authority. Unjoined work retains fences.
No extra scheduler, timer, recovery daemon or general journal framework is added.

Stop/start retains allocations; reset/destroy releases only the selected instance
after consumers stop/join. Attached cancellation, lease expiry and operator/scene
stop retain persistent data and shared allocations under existing attempt/generation
fences. Replacement/recovery never automatically restarts workloads or reattaches
clients. Idle shared services remain until explicit stop; service destruction
refuses all retained allocations, including stopped ones. Native application
cleanup retains main's `observed_empty` confidence and residual unseen-descendant
risk, with known live members, incomplete cleanup or lost authority still fenced.

### Explicit operator cleanup

Expose force only on instance/shared destroy and private manager release, through
command-specific typed requests and transient release authorization. Main's native
`StopRequest`, expected-attempt and scene/attachment contracts remain independent.
Reject unsupported force operations; no fallback endpoint or capability disclosure.
The supplied flag is acknowledgement without another confirmation. Never infer
force for startup, reset, disposable cleanup, recovery or a later retry; persist
ordinary intent without the flag. Requests select recorded IDs, not caller-supplied
SQL names, provider handles or credentials.

Before release, preserve consumer joins, private resource checks and main's
abandoned-data guard, including under force. A native force-stop waiver is not
absence proof or authority to delete shared data; shared force-destroy does not
waive native/filesystem authority. Reject known overlap with retained allocations
and preserve known external consumers. Forced MariaDB release may drop only the
recorded database and exact user-at-host despite comment/authentication/grant
mismatch, after validating identifiers and the exact server endpoint/configuration,
image, mounts, network and loopback binding. Never trust a saved port alone. It may
start that existing server, never create replacements. Verify both SQL objects
absent before settling; failures or acknowledgement gaps retain ordinary intent.

Forced service destruction still refuses every retained allocation. It may remove
only acknowledged recorded server/network IDs and the recorded volume handle,
verifying absence, without forced Docker volume/network removal or implicit volume
removal. Unacknowledged container/network effects require capability recovery or
manual exact-target removal, never same-name adoption. A recorded volume handle
may address an externally replaced volume; explicit force accepts that deletion risk.
External concurrent replacement cannot be locked by Backlot.

Forced instance release may settle selected allocations without recreating SQL
only when authoritative provider observation proves BOTH the exact server and
backing volume absent. Container-only/network-only absence or provider failure is
insufficient. A retained volume with missing server still holds data: restore the
original service manually, or stop all affected consumers, deliberately remove exact
server/storage outside Backlot, then retry forced destruction of each instance.
Instance cleanup never deletes shared storage. Configuration/admin drift that
prevents exact-server verification uses this same explicit maintenance boundary.

Unreadable/lost state cannot supply a bounded target. Stop through retained
authenticated handles or the owning supervisor; retain a private diagnostic backup
and use operator records/provider tools for exact cleanup and absence verification.
A fresh directory does not reconcile old obligations. Never guess/import identity,
scan/prune broadly or signal observed process IDs. Errors exclude driver statements,
DSNs, administrative secrets, capabilities and raw provider payloads.

The small language-neutral manager contract uses one bounded JSON stdin request
and validated stdout result for allocate, inspect/reconcile and release. Ownership
(owned/absent/uncertain) and effect (ready/partial/released) remain distinct from exit
status; force is release-only and unsupported managers fail closed. Backlot owns
intent, deadlines, cancellation and truthful outcomes. Custom command execution,
PostgreSQL 17 and non-Go/non-database evidence remain later PRs; the current types
are provisional contracts, not an implemented custom-provider subsystem.

## Consequences

Worktrees share server overhead with separate data and credentials, at the cost
of engine-specific permission/ownership checks, four private journals and finite
accepted-mutation ownership. Uncertainty can require deliberate maintenance.
Selected startup output intentionally contains selected application secrets under
main's consent contract; administrative credentials stay private. Shared data has
the accepted cooperative-native residual-process risk, without relaxing known
survivor or abandonment fences. Force deliberately accepts recorded-target
replacement risk while requiring actual absence and preserving known peers.

The real v9 fixture and existing transaction failure/interruption tests cover the
upgrade boundary; provider lifecycle tests cover current runtime behavior, without
repeating a provider matrix per historical version. Steps and fixture provenance
remain maintained under the cumulative policy. No new dependency, background
service, machine-wide pool, automatic repair or state reconstruction is introduced.

## Alternatives considered

Per-instance containers remain suitable for stronger separation but do not meet
the requested shared-server overhead goal. Hooks alone cannot verify interrupted
SQL effects. Machine-wide pooling, name-only adoption and automatic relinking cross
unsupported identity boundaries. A plugin framework before additional concrete
managers adds contracts without current consumers.

Permanent refusal fails the explicit recovery requirement. A record-forget command
would falsely settle unknown effects. Manual exact cleanup plus verified absence is
the smaller fallback for missing/unverifiable resources; automatic cascades, state
reconstruction and durable force policy are unnecessary. Requiring reset/fresh state
instead of the existing one-transaction v9 migration discards supported retention.

## Revisit when

PostgreSQL/custom execution supplies concrete protocol evidence; storage/admin
requirements expand; users need cross-daemon sharing or state reconstruction; or
bounded exact-target absence cannot be established. New native containment or
force over native/filesystem authority requires its own decision, not extension of
shared cleanup. No additional present implementation scope follows from these cases.

## References

- [Issue 43](https://github.com/octacian/backlot/issues/43) and [PR 45](https://github.com/octacian/backlot/pull/45)
- [Shared lifecycle](../../internal/daemon/shared.go) and [MariaDB manager](../../internal/manager/mariadb.go)
- [Runtime contract](../containers.md#project-scoped-shared-services-gate-1)
- [Private project configuration](20261021T001732173Z-register-private-project-configuration.md)
- [Cooperative native sessions](20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md)
- [Attached ownership](20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md)
- [Recorded scene targeting](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Native force-stop and abandoned data](20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
- [Retained v9 fixture](../../internal/daemon/testdata/state/v9/README.md)
