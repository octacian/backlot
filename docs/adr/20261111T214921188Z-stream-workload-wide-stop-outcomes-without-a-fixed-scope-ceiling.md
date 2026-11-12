---
title: "Stream workload-wide stop outcomes without a fixed scope ceiling"
date: 2026-11-11
status: accepted
implementation: implemented
summary: "Attempt every safely identifiable workload with individual stop deadlines and live compact outcomes, allowing total work and output to grow with scope."
tags: [lifecycle, admission, api, cli]
supersedes: [/docs/adr/20261106T230626113Z-fence-workload-admission-during-bounded-bulk-stop.md]
superseded_by: []
related: [/docs/adr/20261106T230626113Z-fence-workload-admission-during-bounded-bulk-stop.md]
tracking: [https://github.com/octacian/backlot/issues/140]
implemented_by: [https://github.com/octacian/backlot/pull/141]
---

# Stream workload-wide stop outcomes without a fixed scope ceiling

## Context and Problem Statement

`stop --all` refuses more than 25 targets to fit detailed settlement diagnostics
into a 1 MiB response. Aggregate scan limits and a two-minute coordination deadline
introduce further scope ceilings. Developers expect the command to attempt every
eligible workload, see progress, and inspect individual failures afterward. The
owner prefers total duration, compact bookkeeping and output proportional to scope
over refusing healthy workloads to maintain constant aggregate budgets.

## Considered Options

- Raise fixed limits or add timeout flags: postpones the same failure and requires
  developers to estimate scope before stopping it.
- Keep a finite aggregate response with compact outcomes: removes diagnostic
  overhead but hides progress until the last stop finishes.
- Stream compact outcomes from daemon-owned coordination: reports progress while
  reusing existing cleanup and accepting proportional bookkeeping.
- Add resumable operations and a durable result journal: supports replay but adds
  persistence and recovery obligations the requested workflow does not need.

## Decision Outcome

Replace the earlier bulk-stop size, timing and result commitments with a finite
operation that attempts every safely identifiable eligible workload. Preserve that
record's activity-read contract, admission fence and ticket invalidation, scope,
retention, private cleanup authority, dependent ordering and recovery safeguards.
Activity pagination remains diagnostic and never defines the cleanup selection.

Remove fixed total target, lifecycle-owner, selection-scan and aggregate-byte
ceilings from bulk stop, and remove its total coordination deadline. Allow compact
operation bookkeeping and output to grow with the number of observed targets.
Avoid repeated reconstruction of the same dependency relationships. Keep validation
at trust boundaries and appropriate individual stop, owner-join, lock-wait and
provider deadlines, respecting configured stop grace. A busy lifecycle target gets
a bounded wait and authority revalidation rather than immediate omission. Continue
with independent targets after a failure or timeout; preserve unresolved child
fences on parent resource release.

Select under the workload admission fence, including preceding accepted work,
queued executions, kept services and remaining runtime cleanup obligations.
Protect selected disposable data before cancellation can decide to delete it.
Unreadable state makes selection incomplete; continue only where private ownership
and dependency safety remain provable. An unreadable binding can prevent proving
that a parent is independent. Neither partial selection nor a timeout permits
cleanup inferred from names, public summaries or missing evidence.

Emit typed NDJSON acceptance, compact terminal target outcomes and a final summary.
Human output renders the same events as they arrive. Each selected target has one
reported stopped, failed or unresolved outcome with its identity and a short reason;
a timeout is the coordinator's final observation, not proof cleanup has stopped.
Leave detailed settlement diagnostics to existing inspection. Summarize counts,
failed and unresolved identities, admission joining and selection completeness;
never report all-success for missing or unfinished work. Total output, including
the identity lists, may grow with scope. Missing final summary leaves overall
completion unknown without invalidating previously received target observations.

Require an explicit streaming-contract discriminator before accepting the mutation.
Strict older daemons reject the new request, and new daemons reject missing or
unsupported contracts before effects. Do not silently fall back or retry. Keep the
change local to this operation rather than increasing every endpoint's wire limit.

Separate cleanup ownership from HTTP output: retain compact transient outcomes so
slow or disconnected consumers cannot block coordination. Client interruption ends
observation, not accepted cleanup. Serialize through existing owners, avoid an
unbounded launch of concurrent cleanup, and retain ownership of already-started
cleanup until it finishes, even after reporting a timeout. Daemon shutdown still
cancels and joins registered work under its existing safety contract. Release the
temporary workload fence when coordination settles; success describes this finite
operation, not permanent daemon idleness.

### Consequences

Large scopes take longer and consume proportional compact memory and output.
A failure does not turn later independent targets into unattempted work. Corruption,
unprovable dependencies and uncertain native ownership can still prevent safe
cleanup and require explicit inspection or recovery.

The API/client and CLI change from a finite detailed response to live outcomes;
older peers require matching binaries. Existing authenticated cleanup, retention
and workload journals remain authoritative. Add no durable bulk journal, replay
endpoint, automatic retry, migration or dependency. Transient outcomes disappear
with their observation lifecycle; after a lost stream, inspect existing activity
and instance evidence rather than claiming the original selection completed.
