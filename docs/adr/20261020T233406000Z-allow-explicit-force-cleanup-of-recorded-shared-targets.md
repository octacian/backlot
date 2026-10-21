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

This supersedes [the original shared-allocation decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
only to add the explicit operator exception below. Its unchanged architecture is
incorporated by reference; [implementation status](../implementation-plan.md#shared-services-issue-43)
remains separate from decision acceptance.

## Context

The [original context](20261019T050443877Z-add-project-scoped-shared-service-allocations.md#context)
establishes the need for independent worktree data with shared SQL overhead and
explicit privilege, ownership and recovery obligations.

The owner reopened Gate 2 because ownership uncertainty could leave cleanup
permanently blocked. In particular, release currently requires a verified server
start while server destruction refuses retained allocations. A missing acknowledged
server or volume can therefore trap both commands. An explicit operator instruction
may waive resource ownership proof; unavailable providers and unreadable state do
not establish either a target or deletion. This requirement is additional to the
original issue's automatic preservation contract.

## Decision

Retain the [original Decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md#decision)
in full except where the explicit exception below permits recorded-target cleanup.
That includes project identity and independent daemon roots; exact retained server
configuration and pinned local images; separate capability-owned server/storage/
network and allocation generations; context-correct explicit connection references;
private administration, empty restricted allocations and verified SQL ownership;
state version 7 without migration; durable intent, consumer joins, target-only
release, retained-allocation fences and explicit execution restart after recovery.
The original bounded cooperative manager protocol and later PostgreSQL/custom
execution obligations remain unchanged.

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

The [original consequences](20261019T050443877Z-add-project-scoped-shared-service-allocations.md#consequences)
remain: shared overhead with separate credentials, engine-specific permission
proof, conservative maintenance and no automatic pooling/adoption.

The operator can intentionally delete an externally replaced recorded target;
that risk is the purpose of force. Known peers and consumers remain protected, but
external concurrent replacement cannot be locked by Backlot. Manual catastrophic
recovery carries operator responsibility and cannot prove reconciliation without
state. Real CLI/daemon/provider evidence must establish both the force escape and
unchanged default refusal before implementation is considered complete.

## Alternatives considered

Retain the [original alternatives](20261019T050443877Z-add-project-scoped-shared-service-allocations.md#alternatives-considered),
including private containers for stronger separation and the rejection of automatic
pooling, hooks-only recovery, speculative plugins and name-only adoption.

Manual SQL deletion followed by conservative release works when the original
server remains verifiable, but fails on missing server/storage and label drift.
Manual exact deletion plus forced verified-absence reconciliation is the smaller
fallback for those cases. A record-forget command would falsely settle unknown
effects; an import/relink/recovery subsystem or durable force policy is unnecessary
for readable records. Keeping permanent refusal alone does not meet the owner's
operator recovery requirement.

## Revisit when

The [original revisit conditions](20261019T050443877Z-add-project-scoped-shared-service-allocations.md#revisit-when)
still apply to PostgreSQL 17/non-Go non-database evidence, storage/admin expansion
and any explicit cross-daemon coordination design.

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
