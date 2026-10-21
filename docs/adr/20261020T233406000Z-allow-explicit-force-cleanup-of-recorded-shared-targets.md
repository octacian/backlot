---
title: 'Allow explicit force cleanup of recorded shared targets'
date: '2026-10-20'
status: accepted
summary: 'Retain conservative shared-service lifecycle defaults while allowing an explicit destroy force flag to waive ownership proof for recorded targets and reconcile verified absence.'
tags: [shared-services, identity, sql, security, lifecycle, cleanup]
supersedes: [20261019T050443877Z-add-project-scoped-shared-service-allocations.md]
superseded_by: []
---

# Allow explicit force cleanup of recorded shared targets

Keep the project-scoped allocation architecture and conservative automatic lifecycle,
but allow `destroy --force INSTANCE_ID` and `shared destroy --force SERVICE_ID`
to authorize cleanup of recorded shared targets despite missing ownership proof.
Force deletes actual effects and verifies absence before settling obligations;
it neither cascades through retained peers nor authorizes unverified success.

This replaces [the original shared-allocation decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
after independent architecture review and authorized acceptance. The original
architecture below is retained with the explicit operator exception at the end
of the decision. Implementation and Gate 2 acceptance remain separate.

## Context

Separate worktrees need independent application data without running an entire
SQL server per scene. Checkout identity and the persistent instance key include
checkout/scene, so neither can identify the shared project. SQL provisioning was
previously outside product scope; sharing introduces explicit privilege, ownership
and recovery obligations that generic image initialization cannot satisfy.

The owner reopened Gate 2 because ownership uncertainty could leave cleanup
permanently blocked. In particular, release currently requires a verified server
start while server destruction refuses retained allocations. A missing acknowledged
server or volume can therefore trap both commands. An explicit operator instruction
may waive resource ownership proof; unavailable providers and unreadable state do
not establish either a target or deletion. This requirement is additional to the
original issue's automatic preservation contract.

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

### Explicit operator exception

Expose force only on the two existing destroy commands and private manager release.
Use command-specific typed destroy requests and a transient release force boolean;
reject it on other operations. The supplied flag is the acknowledgment, without a
second confirmation. No default, reset, disposable cleanup, startup or crash recovery
may infer force. Public requests contain recorded instance/service IDs, never SQL
names, provider handles or credentials supplied to redirect cleanup.

For the selected instance, stop/join consumers and serialize with the existing
instance/service locks. Persist ordinary release intent before effects. Forced
MariaDB release may drop the recorded database and exact user-at-host despite
comment, authentication or grant mismatch. Validate identifier shapes, server target
and private state first; reject known overlap with another retained allocation.
Waive ownership proof, not observation: verify both objects absent after deletion.
Failure, cancellation, unavailable administration or an acknowledgment gap retains
ordinary intent for explicit retry. Force is not durable authority: save requests
without the flag, and require another explicit flag for another forced attempt.

Forced shared destruction still refuses every retained allocation. For its recorded
container/network IDs and volume handle, labels may cease to authorize ownership
but the explicit flag does. Stop and inspect the exact server, remove only recorded
objects without forced Docker volume/network removal or implicit attached-volume
removal, and verify each absent before acknowledging removal. Preserve known
external consumers, existing shutdown ordering and native guardian authority.
Unacknowledged container/network effects require capability-based recovery or
manual exact-target removal, never adoption of a same-name replacement under force.
Docker volume names are the provider's recorded handles; the flag accepts the risk
that an externally replaced volume at that exact handle may be removed.

Force allocation release does not require recreating missing resources. With valid
state, an authoritative provider observation that both the exact recorded server
container and backing volume are absent establishes loss of this server's SQL
effects; settle only the selected instance's allocations using the existing
absent/released result. Container absence alone, network absence, an unavailable
provider or a live server with missing storage is insufficient. A retained volume
with missing server still contains data: restore the original service manually, or
after stopping all affected consumers deliberately remove that exact server/storage
outside Backlot, then retry forced instance destruction for each affected instance.
Do not delete shared storage from instance force cleanup. This breaks the retained
allocation/server-destroy cycle without an automatic cascade or database surgery.

Force SQL release may bypass shared resource capability-label mismatch for already
recorded handles, but must still establish the exact server configuration, image,
mount, network and loopback binding before connecting; never trust a stale saved
port. It may start only that existing server, without creating replacements. If
configuration/admin drift prevents this, return actionable failure and use the
manual restoration or exact storage-removal path above. This protects peers and
prevents destructive SQL being sent to an unrelated endpoint.

Irrecoverable loss or corruption of the entire state database is outside these
commands: no readable record means no bounded CLI target. Stop the daemon and
consumers through retained authenticated handles or their owning supervisor, retain
a private backup for diagnosis, and use operator records/provider tools to identify
and delete exact effects and verify absence. Start a fresh state directory only
with explicit accounting for leftovers. Never claim old obligations were reconciled,
import guessed identities, scan/prune broadly, or signal observed process IDs.

Reuse existing intent, result and removal acknowledgments. Keep state version 7:
the transient authorization is omitted from persistence and existing absent/released
facts retain their meaning. No migration, store, worker, timer, dependency or generic
ownership-policy mechanism is added. The still-provisional manager protocol gains
release-only authorization before Gate 3 custom execution; unsupported managers
must fail rather than silently ignore force. Administration/capabilities remain
private; return bounded safe errors, not driver statements, DSNs or raw provider
payloads. Existing private-resource filesystem and native ownership checks remain.

## Consequences

Worktrees share server overhead while retaining separate data and application
credentials. SQL managers introduce engine-specific responsibility and require
real permission/isolation evidence. Configuration conflicts and uncertain ownership
can require explicit maintenance rather than automatic repair. Separate daemon
state roots and external servers are never automatically combined or adopted.

The operator can intentionally delete an externally replaced recorded target;
that risk is the purpose of force. Known peers and consumers remain protected, but
external concurrent replacement cannot be locked by Backlot. Manual catastrophic
recovery carries operator responsibility and cannot prove reconciliation without
state. Real CLI/daemon/provider evidence must establish both the force escape and
unchanged default refusal before implementation is considered complete.

## Alternatives considered

Per-instance generic containers remain appropriate for privileged workloads and
stronger separation. Machine-wide automatic pooling merges unrelated configuration
and identity boundaries. Setup/teardown hooks alone cannot verify interrupted
multi-step effects. A broad plugin framework before concrete engine/non-database
evidence adds unsupported contracts. Name-only adoption or automatic relinking
can reclaim foreign resources and is rejected.

Manual SQL deletion followed by conservative release works when the original
server remains verifiable, but fails on missing server/storage and label drift.
Manual exact deletion plus forced verified-absence reconciliation is the smaller
fallback for those cases. A record-forget command would falsely settle unknown
effects; an import/relink/recovery subsystem or durable force policy is unnecessary
for readable records. Keeping permanent refusal alone does not meet the owner's
operator recovery requirement.

## Revisit when

Use PostgreSQL 17 and a non-Go non-database command fixture to refine the minimal
protocol. Expand storage/administrative configuration only with concrete needs and
review. Revisit sharing across daemon roots only with an explicit machine-wide
coordination design; never infer sharing from matching images or project names.

If evidence shows the bounded storage-absence proof cannot be established, or
operators need state reconstruction or force over native/filesystem authority,
reopen scope rather than adding recovery machinery implicitly.

## References

- [Original shared-allocation rationale](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
- [Draft PR 45](https://github.com/octacian/backlot/pull/45)
- [Shared lifecycle and journals](../../internal/daemon/shared.go)
- [MariaDB effects](../../internal/manager/mariadb.go)

- [Issue 43](https://github.com/octacian/backlot/issues/43)
- [Product requirements](../prd.md#project-scoped-shared-services)
- [Runtime contract](../containers.md#project-scoped-shared-services-gate-1)
- [Implementation status](../implementation-plan.md#shared-services-issue-43)
- [Existing lifecycle decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Offline contracts](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
- [MariaDB database comments](https://mariadb.com/docs/server/reference/sql-statements/data-definition/create/create-database)
- [MariaDB user authentication](https://mariadb.com/docs/server/reference/sql-statements/account-management-sql-statements/create-user)
