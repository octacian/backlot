---
title: "Reassociate retained projects through explicit current bindings"
date: 2026-11-03
status: accepted
summary: 'Separate retained ownership from explicitly reviewed current location bindings so moved or identity-changed projects can reuse data without automatic adoption.'
tags: [identity, shared-services, state, migration, configuration, cli, recovery]
supersedes: [20261019T050443877Z-add-project-scoped-shared-service-allocations.md, 20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
superseded_by: []
---

# Reassociate retained projects through explicit current bindings

Keep retained ownership immutable and add direct current-location bindings so moved
projects reconnect data without rewriting authority. Allow explicit, preview-bound
reassociation to one retained project, preserving service/instance IDs, allocations,
credentials, data and cleanup capabilities. Commit daemon associations atomically;
private setup remains a separate prerequisite.

## Context

Shared project IDs hash filesystem identity, relative location and declared name;
services derive IDs from and embed that identity. Independent checkout/scene/private
fences mean path/ID rewrites can invalidate authority or leave another fence unresolved,
even after service destruction.

The requesting checkout and its per-worktree Git directory differ from the resolved
Git common-directory sharing anchor. Show the actual anchor location, even when it
lies under another checkout; location does not prove permanence. Changed device/
inode values indicate identity change, not proof of replacement. Matching paths,
names, remotes or history do not authorize adoption; independent clones stay separate.

## Decision

### Retained ownership and current selection

Preserve shared ownership records, their hashes, embedded service identities and
original bidirectional path index separately from active indexes. Add one direct
current shared-project binding to a retained project ID and one current checkout
binding to its original registration ID, reused as the stable parent for instances
and persistent slots. Bindings start at the recorded identity and are replaced
explicitly; no merges, alias chains, universal identity type or binding history.

Resolve current bindings before ordinary registration and service lookup. Retained
origin IDs remain reserved: an active-binding miss cannot reactivate an origin
whose current binding differs, even at its original path or after all services are
destroyed. Conflicting path/observed-ID claims, occupied slots and ambiguous owners
refuse repair. Project-name and relative project/manifest-location changes remain
outside this repair; never implicitly relocate another registered project.

Keep current association metadata separate from recorded launch facts. Selection
uses current bindings; launch validation and recorded cleanup retain historical
authority. A newly accepted attempt atomically records its current plan and observed
checkout without changing its stable parent/slot or old attempt/export provenance.
Exact-ID restart/reset uses current associations; destroy uses stable parents while
preserving identity reservations and existing history/evidence erasure semantics.

### Preview and scope

Expose typed `project diagnose` and `project reassociate` contracts; reassociation
without apply is read-only. Scene doctor exposes shared-registration conflicts even
when configuration cannot compose. Show recorded/current checkout paths and IDs,
per-worktree Git directory, resolved sharing anchor, relative project location,
project IDs and available device/inode values; label historical unknowns honestly.
Show what changed, affected services/instances/allocations (including empty retained
registrations), exact association changes, preserved authority and all blockers.
Text/JSON retain existing [privacy rules](20261019T050443877Z-add-project-scoped-shared-service-allocations.md).
Apply selects exact retained owners/destinations and acknowledges the specific
preview through `--apply --preview DIGEST`. Hash a canonical safe actionable
projection of selectors, observations, affected IDs/status, daemon generation and
opaque binding/private-selection revisions. Exclude raw inputs and unkeyed
secret-derived fingerprints. A generation-private random memory key supplies opaque
source-byte freshness revisions, including manual edits; no persisted preview/key
or expiry worker. Recompute before commit; relevant change requires a fresh preview.

Enumerate every peer affected by a shared-anchor change and require all consumers
stopped/joined, locked and fenced, even with unavailable sources. Only explicitly
selected destinations compose; validate their shared declarations/providers against
retained service authority. Never compose a peer from the requesting branch.
Preserve unresolved peer bindings/snapshots and fence their starts until separately
verified/repaired against the new shared binding revision. Recorded cleanup remains
available. Refuse live, kept, pending or uncertain consumers under existing
[lifecycle rules](20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md);
status or a force-stop waiver is not absence proof. A shared SQL server may remain
running under its service lock: repair changes no provider effects or allocations.

### Private setup and compatibility

Keep format-1 private setup and its publication protocol independent. A stale
association blocks apply with the recorded/current association, selected generation,
reason and `project setup --reassociate` prerequisite using explicit values; never
import old credentials implicitly. Unsafe/missing required setup or unreviewable
provenance blocks; ordinary input-free unregistered projects remain valid. Setup
may commit while daemon repair remains unapplied: report that boundary and require
a fresh preview, with no cross-store journal, rollback or compensation promise.

Diagnosis honors the operation's explicit registry root without fallback. Preview/
apply requires that root and ordinary daemon runtime selection to agree, including
canonicalization and safety checks; mismatch is a prerequisite blocker. No persisted
root override or global environment mutation. After daemon restart, use its actual
selection/generation and reject old previews; later operator root changes remain
configuration changes under [private-configuration rules](20261021T001732173Z-register-private-project-configuration.md).

Only association/selection metadata relocates: current checkout path/observed ID,
per-worktree Git-directory path, shared anchor path/filesystem observation and
project/manifest paths derived from unchanged relative locations. Protected resource,
provider, initialization, input, image and mount checks remain unchanged in preview,
apply and subsequent start/restart. Compare the latest accepted launch snapshot,
including after A-to-B/start-at-B/B-to-C; never rewrite snapshots or digests.

Fresh-only initializer comparison includes the resolved executable. An unchanged
`./bin/setup` resolving from A/bin/setup to B/bin/setup therefore blocks this version,
as do changed protected absolute tools or secret paths. Report safe field categories,
never secret values or destruction as successful repair. Moves with unchanged
protected fields are supported; not every source-identical move is. No normalizer,
secret/path rebasing or implicit relative-tool relocation exception is authorized.

### Commit, recovery and migration

Under existing mutation ownership, try a fixed sorted instance lock set once, then
take cancellable ordered service locks and the private publication lock. Verify
sources before acquiring the admission gate last; contention or new affected
registrations/consumers refuses/repreviews, never expands waits. Recheck the complete
set/revisions under the gate; never acquire it holding `service.mu` or hold either
over source reads/lock waits. Old captures recheck revision at acceptance and allocation.

One bbolt transaction revalidates database relationships, changes all selected
daemon bindings/indexes and records their revision/accepted digest. No transaction
spans configuration reads, lock waits or provider/network effects. Precommit failure
changes no association; missing postcommit reply means unknown completion, never
automatic retry. Inspection exposes the accepted digest/current binding; recovery
sees old or new state and never starts consumers. External filesystem/Git/manual
edits cannot be locked by Backlot; later starts reverify and refuse changed sources.

Add only the two binding stores, stable instance parents, revisions/verified shared
revision and accepted acknowledgement. Follow the [cumulative migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
from source12, retaining older steps/decoders. Derive bindings solely from retained
state, never current files or private registry; preserve historical authority and
unrelated bytes, credentials, capabilities, allocations, history and passive files.
Startup validates origin authority plus unique active claims, binding parents and
slots independently of historical launch locations. Preserve legitimate destroyed-
slot gaps and all-step rollback; historical migration steps cannot require future
binding storage. No provider/manager change, new dependency, worker or machine pool.

## Consequences

Bindings add indirection across selection, acceptance and cleanup, migration and
historical-reader/index maintenance; reads/startup validation apply even without
repair. Staged setup and strict compatibility can require another action or refuse
a move without discarding data. Bindings express trusted-user intent, not object
permanence or isolation from hostile same-user edits.

## Alternatives considered

- Diagnostics/setup/cleanup or a fresh checkout cannot reconnect retained scene
  allocations; destruction/fresh daemon state defeats preservation.
- Transactionally rewrite identities and dependent records: keeping IDs still needs
  origin facts; rekeying changes service/allocation/provider parents. Direct bindings
  preserve those authority-bearing records and hashes with fewer historical writers.
- Atomic private-and-daemon repair needs cross-store recovery and offline-writer
  fencing. Existing staged setup provides a smaller, truthful failure boundary.
- Automatic path/name/Git matching cannot establish intended ownership or safe
  credential reuse. Shared-only bindings leave independent checkout fences unresolved.

## Revisit when

One-step cross-store repair, project renaming/relative relocation, provenance-backed
relative-tool relocation, cross-daemon sharing, binding history/reversal, hostile
same-user isolation or automatic relocation is required; or the two concrete
bindings and existing locks cannot preserve coherent current/historical selection.

## References

- [Shared identity](../../internal/daemon/shared_identity.go), [authority validation](../../internal/daemon/state_authority.go), [scene selection](../../internal/daemon/scene.go)
- [Private publication](../../internal/projectconfig/store.go), [attempt acceptance](../../internal/daemon/runtime_state.go), [history/erasure rules](20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)

This supersedes only move/replacement refusal and location-selection rules in the
linked shared-service/scene decisions; their other guarantees continue in force.
