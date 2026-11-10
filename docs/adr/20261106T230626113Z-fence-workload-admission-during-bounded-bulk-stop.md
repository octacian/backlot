---
title: "Fence workload admission during bounded bulk stop"
date: 2026-11-06
status: accepted
implementation: in-progress
summary: "Expose bounded recorded activity and stop selected workloads behind a temporary admission fence while retaining data and administrative services."
tags:
  - lifecycle
  - admission
  - api
supersedes: []
superseded_by: []
related: [/docs/adr/20261105T063300829Z-run-declared-native-tasks-against-accepted-ready-scenes.md, /docs/adr/20261028T234445000Z-release-stopped-instance-networks-while-retaining-application-data.md, /docs/adr/20261027T215913789Z-separate-declaration-discovery-from-bounded-recorded-inspection.md, /docs/adr/20261106T230242771Z-recover-device-only-bindings-after-verified-consumer-settlement.md]
tracking: ["https://github.com/octacian/backlot/issues/98"]
implemented_by: []
---

# Fence workload admission during bounded bulk stop

## Context and Problem Statement

Paginated instance history does not identify every current consumer or explain
gateway and shared-service blockers. Repeated per-instance stops can miss queued
or concurrently admitted work. Daemon shutdown provides a permanent admission
fence but also stops administration; developers need to stop workloads while
leaving the daemon, gateway and shared servers available.
Logical project registration, current consumer settlement and shared-service
transition causes already exist under the accepted registration decision. This
decision composes those systems; it does not redefine identity or reconciliation.

## Considered Options

- Keep list/status plus caller-side stop loops: smallest runtime change, but no
  coherent admission boundary or bounded aggregate blocker contract.
- Temporarily fence workload admission and reuse owned execution/cleanup: meets
  the workload-only boundary without another durable operation lifecycle.
- Reuse daemon shutdown and restart administration: retains data but interrupts
  infrastructure and cannot provide the requested workload-only operation.
- Persist resumable bulk operations or an activity index: improves replay and
  large-state selection, but adds writers, recovery and retention without a
  demonstrated need.

## Decision Outcome

Provide typed finite `backlot activity` reads and explicit `backlot stop --all`,
scoped only to the selected daemon/state directory. Existing single-target
contracts remain unchanged. Activity combines recorded workload, route, allocation
and cleanup obligations with current daemon-owner observations. Include starting,
queued, kept and uncertain work, not only ready instances. Expose exact recorded
blocker identities and safe next commands for gateway stop/restart and shared
stop/restart/destroy, using the same predicates as those lifecycle guards. A
retained allocation can block destroy without blocking stop. Preserve the
attempt-bound route-only recovery exception; aggregate cleanup failure alone is
not proof of a live consumer.

Reuse `ConsumerSettlement` and its existing evaluator for current consumer
eligibility and blocker diagnoses. Preserve historical status, completion,
collection gaps and worst native confidence: `interrupted` can coexist with
verified current settlement. Public settlement is diagnostic, never cleanup
authority; mutations revalidate private attempt/generation-bound release proof
against the exact current effects and join live owners. Preserve unknown legacy
evidence and existing abandonment and destructive-cleanup safeguards. Surface
shared services' existing `last_transition` and cause without creating another
transition history or changing observations on reads.

Label recorded intent, current owner observations, verified cleanup and uncertainty
separately. Recorded ports/origins and server status do not prove live listeners or
provider readiness. Reads do not probe providers, allocate, reconcile or reveal
private capabilities, credentials, commands or hydrated inputs. Paginate with
explicit scan, byte and result limits, including skipped records; incomplete pages
and changing membership never establish global absence. Summaries can direct
callers to existing detailed inspection rather than embed unbounded results.
Use recorded immutable owners and their existing registration/binding context;
activity and bulk stop require no enrollment, marker repair or source recomposition.
Ordinary marker recognition remains independent of consumer settlement. Explicit
registration repair retains its existing locks, preview/revalidation and consumer
fences; the workload fence does not grant new repair authority.

Register preparation and every workload-starting mutation with the existing
daemon mutation ownership before acceptance. This includes ordinary/attached
run, scene start, task run, restart and reset. Atomically close workload admission
under the existing service mutex before selecting targets; reject new admissions
and invalidate earlier admission tickets permanently, including requests still
resolving inputs or waiting for an instance lock. Reopening cannot make an old
ticket eligible again. Check that authority at preparation, destructive restart/
reset boundaries and execution registration. Cancel and boundedly join preceding
admission owners; a timeout is unresolved, never permission for a late launch.
Already registered executions waiting for outputs are selected and cancelled too.
Read, renewal, ordinary cleanup and explicit infrastructure controls remain usable.

Use a bounded complete selection of recorded active preparations/executions,
kept consumers and remaining runtime cleanup obligations, using current settlement
and existing recovery selectors rather than historical status alone. An uncertain
record without actionable cleanup evidence remains visible and unresolved; bulk
stop cannot manufacture release proof. Account for dependent settlement scans
within the operation's bounds. Refuse oversized or
unreadable selection before issuing target stops; never silently stop one page
and call it all. Serialize each selected attempt with existing mutation ownership,
join dependent tasks before parent resources, and reuse authenticated native,
container, route and network cleanup, including the existing ownerless interrupted
stop/recovery path and its release producers. Do not add a second reconciliation
engine. Busy targets and exhausted budgets remain
explicitly unresolved. No bulk force bypass or ownership inferred from public
history, names, PIDs or transport loss is permitted.

Bulk cancellation retains selected application data, paired credentials,
allocations and diagnostic history, including disposable runs. Apply an explicit
per-execution bulk-stop cause before cancellation: suppress both keep-on-failure
and cancellation-triggered disposable data deletion, reusing shutdown's retention
policy without its global closing flag. Ordinary disposable completion/cancellation
keeps its existing policy. This operation cannot undo cleanup or destruction
already begun by previously accepted work, nor prevent applications' own writes.
Release only authenticated runtime resources whose consumers have joined. Idle
retained data is not a cleanup target; shared servers, gateway and daemon are not
stopped, and foreign resources and other state roots acquire no new authority.

Give the finite operation an identity within the daemon generation and register
its cancellation/join ownership with daemon shutdown. Client interruption or
disconnect stops waiting, not accepted cleanup. Bound coordination and stop attempts.
Existing execution owners may continue after a bounded join; busy targets receive
no new cleanup attempt and remain unresolved. Any started kept-service or recovery
cleanup retains a registered, shutdown-joined owner and its serialization until
it finishes, even after the coordinator returns. Bind continuing cleanup to the
selected attempt and resource generation; reopening admission never authorizes it
to act on a successor. Durable retry obligations do not replace ownership of live
cleanup. Return per-target completed, failed or unresolved outcomes with current
settlement, preserving collection errors
separately from surviving consumers. Partial failure is nonzero with its usable
typed result. Release the temporary fence when the coordinator settles, including
failure, unless daemon shutdown has permanently closed admission. Success means
the selected operation completed, not that the daemon remains idle afterward.

Do not persist a bulk journal, replay results or resume a bulk operation after a
crash. Existing workload journals and startup recovery remain authoritative.
After a lost reply, inspect activity and known targets in the same scope before
explicit recovery; never automatically retry or claim the original target set was
completed. No event stream, index, timer, provider framework or dependency is added.

### Consequences

The change reaches all admission writers, teardown policy and blocker predicates,
as well as shared contracts, client validation, CLI output/help and operating/skill
guidance. The temporary fence exists only while coordination runs; any continuing
cleanup owner remains registered until its work finishes.
Ordinary workload/history writers and retention continue unchanged when unused.
Reuse the current registration, release-proof and transition storage; this feature
adds no state-format migration. Large scopes may require explicit targeted
stops; incomplete ownership can still require existing operator recovery. These
limits avoid a durable bulk scheduler while preserving truthful partial outcomes.
