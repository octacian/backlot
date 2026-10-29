---
title: 'Retain bounded attempt history and erase it on successful destroy'
date: '2026-10-28'
status: accepted
summary: 'Retain attempt-scoped public outcomes and evidence under bounded daemon retention so replacement preserves diagnosis and successful destroy erases retained history.'
tags: [daemon, lifecycle, evidence, state, api, retention]
supersedes: [20261018T233600000Z-publish-daemon-owned-portable-run-reports.md, 20261027T215913789Z-separate-declaration-discovery-from-bounded-recorded-inspection.md, 20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md, 20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
superseded_by: []
---

# Retain bounded attempt history and erase it on successful destroy

Retain public attempt outcomes and evidence independently of later executions,
using existing daemon storage and retention. Exact inspection and export preserve
diagnosis across replacement; successful explicit destroy erases retained history.
Export evidence before destroying its instance.

This replaces latest-only report selection, recorded inspection's no-history
limitation, and scene-history retention after destroy in the superseded records.
Their unaffected publication, privacy, image-consent, recovery and gateway-waiver
contracts continue by reference. Start eligibility is a separate decision.

## Context

One replaceable result/runtime journal loses predecessor outcomes and launch
provenance. Aggregate logs cannot reconstruct them reliably or safely support
independent attempt erasure. Repeated failures need durable diagnosis without
archiving secret runtime state or adding an event-replay system.

## Decision

Retain an intentional public projection keyed by instance/attempt, ordered by a
transactional per-instance acceptance sequence. Replacement atomically preserves
the predecessor and installs the new attempt with its matching public projection.
Every result writer updates its matching attempt in the same transaction; stale
writers cannot change another attempt. Keep original terminal outcomes separate
from later cleanup observations. Completion does not establish cleanup safety.

History includes bounded outcomes, known timestamps and export provenance, never
plans, private snapshots/configuration, fixtures, secrets or capabilities. Private
runtime journals alone authorize cleanup. Application logs/artifacts retain their
existing output sensitivity; history creates no new cleanup authority.

Provide bounded, paginated newest-first history, exact inspection and last-completed
selection through separate typed contracts. Live keyset pages promise no snapshot;
removal never redirects selection. Last-completed ignores newer incomplete work.
Selected logs/export use only that attempt's outcomes, provenance and evidence,
never current-plan facts or current-attempt fallback. Missing/pruned selections are
unavailable; unknown legacy facts remain unknown. A settled predecessor can export
while newer work runs; active/kept producers remain ineligible. Existing export
auditing, partial-mode limits and no-replacement publication remain unchanged.
See [read/export contracts](../evidence.md#retained-attempt-history).

Automatic age/byte expiry removes evidence while preserving metadata and explicit
expiry facts. Completed-count and manual pruning erase selected history/evidence;
manual age/count selectors form a union and apply rechecks protection. Active,
kept or unresolved-cleanup obligations are never pruned, so caps may be exceeded.
Pruning never removes application data, credentials, snapshots or shared allocations.
The current snapshot remains current-state truth after its completed history row
is pruned; later writes cannot resurrect that row. Defaults and selectors belong
to the [retention policy](../evidence.md#logs-and-retention).

Persist deletion/expiry intent before file effects and retry incomplete erasure
under existing mutation/evidence fences and consistent lock ordering. No database
transaction spans file copying/removal. Attempt-scoped new logs avoid rewriting
live aggregates; fenced legacy rewrites preserve unselected records and visibly
invalidate affected cursors. Legacy unrepresented bytes expire by persisted
completion/observation clocks, never mtime or restart-renewed age. Preserve
protected/unexpired evidence and unknown bytes while cleanup remains unresolved;
do not invent results or completion times to account for them.

Successful destroy joins producers, completes owned runtime/data cleanup, then
erases all retained attempt records/logs/artifacts and private original-result
copies, including resolved native-abandonment audits. Leave only an identity
and recorded-targeting tombstone without execution payload. Caller exports survive.
Failed/uncertain cleanup retains diagnosis; evidence-erasure failure is failure,
with durable retry intent. Reads never erase history.

Require the fixed `DestroyRequest` contract `erase-attempt-history/v1`, including
force and retries. Missing/unsupported markers are rejected before admission,
durable mutation or runtime/data/evidence effects. Matching clients supply it and
accept execution-free destroyed tombstones; older clients must upgrade. Old strict
daemons reject the unknown field. No marker-free fallback is permitted; lost or
unusable mutation replies mean unknown acceptance/completion. This narrow gate is
necessary because configuration preflight does not protect old destroy callers.

Add the genuine v11-to-v12 database-only migration to the retained cumulative
v9 chain, grounded in a nonempty closed database from the unmodified pinned v11
producer. Historical steps validate their own source layout. Seed only known current
results and matching provenance; overwritten outcomes cannot be reconstructed.
Legacy bytes retain their expiry policy without invented results; adaptation of
files occurs after open under existing ownership, outside migration transactions.

## Consequences

All accepted executions incur transactional history writes even without readers.
Existing retention sweeps also retry erasure; no dependency, worker or timer is
added. Protected cleanup can grow storage beyond policy bounds. Destroy becomes
irreversible evidence erasure, requiring matching clients; export first. Existing
native cleanup confidence and unseen-descendant limitations remain unchanged.

## Alternatives considered

- Latest-only results or caller exports alone cost less but lose diagnosis when
  replacement wins the race to preserve evidence.
- Whole-journal copies expose secrets/capabilities and confuse history with authority.
- Event sourcing, SQL or another retention worker add machinery without a replay need.
- State preflight alone leaves old destroy calls unprotected; global negotiation
  is broader than the fixed pre-effect request gate needed here.

## Revisit when

Protected storage growth or measured lifecycle/query costs become material, or
consumers require transition replay rather than bounded outcomes.

## References

- [Issue 99](https://github.com/octacian/backlot/issues/99)
- [Cumulative migrations](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Destruction](../evidence.md#investigation-and-destruction), [portable export](../evidence.md#portable-per-instance-export)
