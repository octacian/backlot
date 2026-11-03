---
title: "Reassociate retained projects through explicit current bindings"
date: 2026-11-03
status: accepted
implementation: in-progress
summary: "Separate retained ownership from explicitly reviewed current bindings so moved or identity-changed projects can reconnect data without automatic adoption."
tags: [identity, shared-services, state, migration, configuration, cli, recovery]
supersedes: [/docs/adr/20261019T050443877Z-add-project-scoped-shared-service-allocations.md, /docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
superseded_by: []
related: [/docs/adr/20261021T001732173Z-register-private-project-configuration.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md, /docs/adr/20261103T174849392Z-select-bounded-working-directories-for-native-commands.md]
tracking: []
implemented_by: [/internal/daemon/reassociation.go, /internal/daemon/project_bindings.go, /internal/daemon/reassociation_native_test.go]
---

# Reassociate retained projects through explicit current bindings

## Context and Problem Statement

Filesystem-derived project IDs also identify services and embed their authority.
Independent checkout, scene and private-configuration fences make identity rewrites
unsafe or incomplete. Users need to reconnect retained allocations after an explicit
move or identity change without discarding data, credentials or cleanup authority.

The requesting checkout, its per-worktree Git directory and the resolved Git common
directory are distinct. Show the actual sharing anchor, including when it lies under
another checkout. A path does not prove permanence; device/inode drift indicates
identity change, not proof of replacement. Names, paths, remotes and history cannot
authorize adoption or merge independent clones.

## Considered Options

- Direct current bindings to immutable owners, coordinated across checkout selection
  and shared services: preserves authority with two bounded stores.
- Rewrite/rekey identities and dependent records: changes provider parents or still
  needs origin facts to preserve IDs and historical authority.
- Diagnostics, fresh state or destruction: cannot reconnect retained allocations.
- Shared-only repair: leaves independent checkout/private fences unresolved.
- Atomic private-and-daemon repair: adds cross-store recovery and offline-writer
  fencing; existing staged setup provides a smaller, truthful failure boundary.
- Automatic matching: cannot establish intended ownership or safe credential reuse.

## Decision Outcome

Keep original shared-project records, hashes, embedded service identities and
bidirectional path indexes immutable. Add one current shared-project binding to its
retained owner and one current checkout binding to its original registration, which
remains the stable parent of instances and persistent slots. Replace bindings only
through explicit repair; no merges, alias chains, universal identity or history layer.

Resolve current bindings before ordinary registration/service lookup. Origin IDs and
paths remain reserved even after all services are destroyed: a binding miss cannot
reactivate a relocated origin. Reject foreign claims, ambiguous owners and occupied
slots. Preserve declared project and relative project/manifest locations; never
implicitly relocate another registered project or pair independent retained owners.

Keep current selection separate from recorded launch facts. Acceptance atomically
records the new plan and observed checkout while retaining stable parents/slots,
service/allocation IDs, credentials, capabilities and historical export/cleanup facts.
Exact-ID restart/reset uses current association; destroy retains reservations and
existing history/evidence erasure semantics.

Diagnosis and preview are read-only, expose recorded/current identities, affected
services/instances/allocations and exact changes/preservation, and label unknown facts.
Scene doctor exposes identity/verification conflicts before private composition.
Apply selects exact retained owners and acknowledges a canonical safe preview of
selectors, observations, affected state, daemon generation and opaque revisions.
Exclude raw inputs and unkeyed secret fingerprints; generation-private memory-keyed
source freshness covers manual edits without a persisted preview/key or expiry worker.
Recompute before commit; stale or conflicting state requires a fresh preview.

Enumerate all affected peers, including consumers without allocations. Require stopped,
joined, independently settled consumers and a fixed locked/fenced set; waiver alone
is not absence proof, and historical gaps/warnings remain. Compose only selected
destinations and check declarations/providers against retained authority. Preserve
unresolved peer bindings/snapshots and refuse their starts until separately verified;
never substitute the requesting branch. Recorded cleanup remains available, and a
shared SQL server may remain running without changing provider effects or allocations.

Private setup remains separate, using explicit values without credential import.
Unsafe/missing setup or unreviewable provenance blocks; ordinary input-free
unregistered projects remain valid. Diagnosis honors the explicit registry root;
apply requires agreement with ordinary daemon selection, including after restart.
No persisted root override, global environment mutation or cross-store rollback
promise: setup may succeed while daemon repair stays unapplied. Restart invalidates
old previews and later root changes remain ordinary configuration drift.

Relocate association metadata only. Compare protected resource/provider/initializer,
input/image/mount fields against the latest accepted snapshot, including repeated
repairs, without rewriting snapshots/digests or rebasing paths/secrets. Preserve
ordinary default-cwd equivalence and strict nondefault fresh-only cwd checks. A moved
relative fresh-only executable or nondefault effective initializer cwd can therefore
block repair even with identical declarations. Report safe categories, not secret
values or destruction as successful repair; add no relocation normalizer.

Use existing mutation ownership and fixed fail-fast instance locks, cancellable
ordered service/private-publication locks, then the admission gate last. Revalidate
sources before the gate and the complete affected set/revisions under it; new
registrations refuse/repreview rather than expand waits. Never acquire the gate
holding service.mu or span source reads/lock waits with either. Captured starts check
revisions at acceptance/allocation; first service intents share binding admission.
One bbolt transaction revalidates relationships and publishes selected bindings,
revision and acknowledgement, without provider/network effects. Precommit failure
changes no association; missing replies require inspection, never automatic retry.
Recovery sees old or new state and never starts work. External filesystem/Git/manual
edits cannot be locked; subsequent starts reverify.

Follow cumulative source12-to-13 migration from retained state only, preserving older
steps, authority, unrelated bytes, journals, history/passive files and destroyed-slot
gaps. Startup validates original authority, unique active claims, stable parents and
historical launch relationships. Failure/uncertainty preserves ownership with
all-step rollback; old steps cannot depend on future bindings. No new provider,
dependency, worker or machine pool. Only the superseded decisions' move/refusal and
location-selection rules change; their other guarantees remain in force.

### Consequences

Bindings impose indirection and validation on ordinary selection, startup, acceptance
and cleanup even without repair. Staged setup and strict compatibility can refuse a
move while preserving data. Bindings record trusted-user intent, not permanence or
isolation from hostile same-user edits. Revisit for cross-store one-step repair,
renaming/relative relocation, provenance-backed tool relocation, cross-daemon sharing
or binding history, rather than silently expanding these two concrete bindings.

## More Information

[Repair workflow](/docs/cli.md#reassociate-retained-project-data),
[authority checks](/internal/daemon/state_authority.go),
[private publication](/internal/projectconfig/store.go),
[history/erasure rules](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md).
