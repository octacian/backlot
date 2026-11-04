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
implemented_by: [https://github.com/octacian/backlot/pull/120]
---

# Reassociate retained projects through explicit current bindings

## Context and Problem Statement

Filesystem-derived project identities also own services and allocations. Rewriting
those identities risks losing data or cleanup authority, while independent checkout
and private-configuration fences make shared-only repair incomplete. Owners need an
explicit way to reconnect retained data after a move or filesystem identity change.

## Considered Options

- Current bindings to immutable owners: preserves authority across shared and
  checkout selection, at the cost of indirection and validation.
- Rekey identities and dependents: risks changing provider parents or still needs
  original identities to preserve authority and IDs.
- Diagnose then use fresh state or destroy: cannot reconnect retained allocations.
- Automatically match paths, names or history: cannot establish intended ownership.

## Decision Outcome

Keep original ownership and origin reservations immutable, even after the last
service is destroyed. Explicit exact-owner repair changes one current shared-project
binding and one checkout binding; original checkout registrations remain stable
instance/scene parents. Never merge independent owners, automatically adopt moves,
or change declared project names or relative project/manifest locations. The resolved
Git common directory is the sharing anchor, distinct from the requesting checkout
and its per-worktree Git directory; see [identity interpretation](/docs/cli.md#reassociate-retained-project-data).

Separate [current selection from launch history](/docs/daemon.md#identity-and-lifecycle).
Preserve service/allocation IDs, paired credentials, capabilities and recorded
cleanup authority, with existing [history/erasure rules](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md).

Require a safe read-only preview, exact acknowledgement and revalidation before one
atomic daemon publication without provider effects. Freshness is secret-safe and
generation-scoped; restart, stale or concurrent changes require repreview. Precommit
failure preserves associations; uncertain completion requires inspection, never
automatic retry. External edits remain subject to subsequent start validation.

All affected consumers, including pending first-allocation peers, must be stopped,
joined and independently settled; a waiver is not absence proof. Preserve historical
gaps/warnings and unresolved peers' own bindings/launch snapshots; fence their starts
until separately verified, never compose their scene from the requesting branch.
Validate selected declarations/providers against retained authority. The shared SQL
server may stay live, preserving allocations.

Stage private setup separately with explicit values, no implicit credential import,
and agreement between the reviewed registry and daemon runtime selection. Atomic
cross-store repair would require additional recovery/writer fencing; staged setup
may succeed while daemon repair stays unapplied. Compare protected configuration
against the latest accepted snapshot without rebasing secrets or rewriting history.
Even unchanged relative fresh-only executables or nondefault initializer working
directories may block a move; omitted/`.` working directories remain equivalent.

[Binding migration](/docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
uses retained origins and preserves existing authority/history. Only the superseded
decisions' move/refusal and location-selection rules change; all other guarantees
remain in force.

### Consequences

Ordinary selection and lifecycle validation must account for current bindings even
without repair. Staged setup and strict compatibility can refuse moves while
preserving data. Bindings record trusted-user intent, not filesystem permanence or
isolation from hostile same-user edits.
