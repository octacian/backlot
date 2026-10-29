---
title: 'Ensure persistent start accepts one eligible current attempt'
date: '2026-10-28'
status: accepted
summary: 'Make explicit persistent start join running work or accept one compatible new attempt after prior work is reconciled without resetting identity or data.'
tags: [daemon, lifecycle, identity, cli, attachment]
supersedes: [20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md, 20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md, 20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md, 20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md, 20261021T001732173Z-register-private-project-configuration.md, 20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md]
superseded_by: []
---

# Ensure persistent start accepts one eligible current attempt

Make `scene start NAME` and persistent `run NAME` ensure a running scene through
one daemon coordinator: join an accepted start, return ready with unapplied drift,
or accept one new eligible attempt using compatible current inputs. Preserve
persistent identity, data and paired credentials, reconcile/join old work first,
and block uncertain ownership. One explicit invocation requests one attempt;
there is no background retry or crash continuation. Overlapping ordinary starts
share an accepted attempt only while it remains current. If it has been replaced,
return a typed conflict with history guidance, never another launch or a
historical success response.

This is phase two of [issue 99](https://github.com/octacian/backlot/issues/99),
after delivery/review of [bounded attempt history](20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md).
Accepted after independent architecture review and coordinator approval. This
partially supersedes lifetime's explicit restart after interrupted work, recorded
targeting's unhealthy-start rule, attachment's failure/restart eligibility, startup
disclosure's stopped/start snapshot selection, image-pin recovery's explicit
restart requirement, and the inherited private-configuration restriction requiring
restart for failed/interrupted instances. All unaffected rules remain in force,
including startup disclosure authorization/redaction and exact-attempt polling,
image acceptance/consent, native supervision, private registry/privacy, identity,
lease and cleanup. Phase one's history decision remains accepted and unchanged.

## Context

The current shared `runSelected` coordinator already serves both entry points.
It can return an old failed execution because its in-memory entry remains, uses
the recorded snapshot for ordinary stop/start, and grants new start only from
prepared/stopped states. Explicit restart uses current inputs and compatibility
checks before stopping the old runtime. Merely broadening a status condition would
bypass join, drift, retained-image and attachment boundaries.

The owner wants an explicit start after fixing prerequisites/configuration to make
one new attempt when nothing is running. Ready scenes remain idempotent; deliberate
replacement of healthy work remains restart. Original results/evidence must survive
replacement under phase one's retention. Disposable execution and exact terminal
exits are outside the behavior change.

## Decision

Keep lifecycle policy in `runSelected` and the existing instance mutation lock,
not CLI retries or a second scene coordinator. After admission, identity and
current-input validation, capture the selected instance, observed attempt/status
and existing acceptance highwater together in the preparation/selection transaction.
Carry those facts only in request-local memory across serialization, then re-read
the selected instance under the lock. A separate later highwater read can bind a
queued caller to the wrong successor and is insufficient.
Reconcile/join prior owned work before replacement and recheck eligibility at
transactional acceptance. Do not infer absence from a failed status, missing
in-memory owner, provider error or transport loss.

| Recorded condition | Explicit ordinary persistent start |
| --- | --- |
| No retained identity | Prepare and accept one fresh instance/attempt |
| Preparing | Join preparation, then re-evaluate its result; failed preparation is not proof of safe launch |
| Prepared | Accept one attempt after reconciliation and compatibility checks |
| Starting | Join the same accepted attempt and its readiness result |
| Ready | Return the same attempt, reporting unapplied manifest/config drift |
| Stopping | Join cleanup, then re-evaluate; do not race a producer |
| Stopped, failed, cancelled or interrupted | Accept one new attempt after joined cleanup and compatible current configuration |
| Destroyed | Named run/start creates a fresh identity; explicit restart of destroyed ID remains rejected |
| Any unresolved/fenced ownership or incompatible retained resources | Reject with actionable diagnosis; preserve data and diagnostic evidence |

Persistent `succeeded` is not a supported ordinary ready lifecycle state; an
incoherent retained state remains a typed error rather than implicit launch.
Completion/collection failure alone is not an ownership fence: preserve it in
history, join collectors and require established runtime/resource cleanup before
new acceptance. A cleanup failure or uncertain native claim blocks replacement
until existing explicit recovery/force rules resolve it.
Ordinary run and scene-start requests use the existing cleanup-aware client
request policy, retaining caller cancellation and bounded daemon cleanup;
attachment renewal and expiry timing are unchanged.

Concurrent ordinary invocations are bounded by their pre-lock observation, not
by whether the current attempt is still starting when they acquire the lock.
An observed starting/ready attempt remains the binding even if it becomes terminal.
For an eligible predecessor at acceptance sequence n (including no attempt at
highwater zero), an intervening acceptance binds the caller to exactly n+1, not
whichever attempt is current later. Prove current identity and highwater together
under the mutation fence: current B at highwater n+1 is sufficient even when its
history row was pruned. Return that coherent current snapshot, including terminal
B, without another acceptance. Only consult the exact n+1 history key to recover
an otherwise unknown bound identity for a replacement conflict; never choose the
newest row. Highwater above n+1 conflicts regardless of retained history.
Attached requests instead refuse any intervening acceptance.

If the binding has already been replaced before the daemon response is captured,
return a typed conflict identifying the recorded instance and bound attempt when
available, with guidance to inspect its retained history/logs/export. If its facts
were pruned or destroyed, report unavailability/conflict without fallback. For
example, two callers observe A, one accepts B, B fails, and a later caller accepts
C: the queued A caller conflicts, leaving C intact and accepting no D. If B is
still current and terminal, that queued caller returns B's coherent snapshot.
Only a genuinely later invocation observing eligible terminal B may accept C.

This deliberately weakens the earlier proposed exact-bound historical-result
promise: sharing succeeds while the binding remains current; replacement is an
acceptable visible failure. History remains an independent diagnostic path, not
a successful start response or cleanup authority. Do not archive plans, hydrate
historical success, substitute C's plan, or automatically try again. Missing
mutation replies still mean unknown acceptance, requiring observation/diagnosis.

Keep normal InstanceResponse/SceneResponse and PlanError shapes. Reuse
`startup_conflict` for attempt replacement, through the normal daemon error
envelope and CLI presentation-error fields; use HTTP conflict classification for
that code. Safe messages carry public identities and exact-attempt history guidance,
never snapshot secrets, private configuration paths or capabilities. No new start
contract marker, matching-client gate or response union is needed. Existing API
and state compatibility checks remain unchanged.

CLI readiness polling stays pinned to the returned attempt. If B is replaced by
C before the next poll, return conflict with the last validated B snapshot labelled
last observed; never hydrate a successful summary from history or C. Preserve scene
target/checkout/generation checks. Startup cancellation must send the existing
StopRequest.ExpectedAttempt precondition before effects; comparing a stop response
after an unfenced ID-only stop is too late to protect C. Ordinary joins acquire no
attachment authority; existing operator stop semantics remain separate.

For a newly accepted attempt, resolve current compatible configuration and run the
existing retained-resource, private provenance, initialization, route identity,
shared allocation and image-pin checks. Use the explicit current machine path or
the normal per-user default when omitted; ordinary start must not inherit the
recorded ConfigPath. Existing explicit restart path inheritance is unchanged.
Current inputs mean the single captured resolution for this request, not a promise
that files remain unchanged while queued; retain source-identity verification.
Atomically recheck the observed acceptance revision/eligibility and install that
snapshot with the new attempt, so rejected acceptance cannot publish new launch provenance.
Reuse paired generated credentials and retained data. Protected drift still needs
reset/destroy; never perform those destructive operations as a start fallback.
Ready/starting joins retain recorded runtime configuration and truthful drift,
without reconfiguration or a healthy-work replacement.

Retained build-image pins remain authoritative. Ordinary start does not inherit
restart's permission to accept a changed image ID or waive mounted-data consent.
Preserve explicit `restart --allow-image-change`, same-ID reuse and existing
missing-image diagnosis. Fresh unpinned generations retain their existing image
acceptance behavior. If a configuration change cannot safely use the retained pin,
reject with explicit restart/reset guidance; no consent is inferred from start.

Attachment is granted only to a newly accepted eligible attempt. A concurrent
attached request cannot adopt starting/ready/otherwise unrelated live work. An
ordinary caller can join an attached attempt without renewing or acquiring its
lease. New eligible failed/cancelled/interrupted starts can acquire fresh attachment
after the same reconciliation/drift gates. Existing instance/attempt/generation
capabilities, signal/loss cleanup, replacement fencing and expiry remain unchanged.

Phase one's transaction preserves predecessor outcomes and evidence before new
acceptance; successful responses expose only the bound current attempt. No copying
old terminal exits or collection failures into a new attempt. Durable native-waiver warnings
remain truthful historical warnings. Recovery still stops/reconciles owned work
and records interruption; only a later explicit start invocation launches again.
No daemon restart automatically starts scenes. Preserve disposable lease,
keep-on-failure, arguments, original exits and cleanup behavior.

Keep command/API syntax unchanged. Update executable help, lifecycle tables,
current CLI/daemon/container/evidence guides, adoption/daily-use examples and
installed skill sources/generated references with phase-two implementation.
Until then these shipped-behavior guides remain unchanged.

## Consequences

The change reuses the lifecycle lock, preparation deduplication, bbolt acceptance,
retained-resource checks, attachment owner and phase-one history. It adds no
persistent field, state version, migration/predecessor fixture, worker, timer,
dependency, durable retry state or independent cleanup authority. History writes
remain in phase one's existing transactional hook; selection reads its highwater
and acceptance adopts current configuration through the existing transaction.
When unused, no additional work runs beyond phase-one history/retention; a start
request may now perform reconciliation and compatibility checks before launching.

Operators can restart a failed/stopped scene through explicit start, while healthy
instances still require explicit restart to apply changes. Current compatible
configuration now applies after stopped/start, which is an intentional change from
retained-snapshot reuse. Predecessor fixtures and privacy remain phase-one
obligations; phase two needs race, drift, attachment and retained-data/credential
runtime evidence, with success and failure through actual CLI/client/daemon before
broad suites. Race evidence must queue two ordinary callers observing failed A,
accept B with one caller, let B fail and finish cleanup before releasing the
second caller, proving it returns coherent B with no extra acceptance. Then cover
the full A/B/C race above: C accepted before the queued caller resumes must yield
conflict and no D. Cover replacement between CLI reply and poll, exact successor
pruning/destroy without fallback, attempt-fenced cancellation, and refusal of an
overtaken attached caller. Existing provider/native confidence limits remain unchanged.

## Alternatives considered

- Keep explicit restart for every unhealthy attempt: smallest code but preserves
  the reported operator trap and fails the approved ensure-running behavior.
- Return the exact historical bound outcome after replacement: stronger convergence
  needs a distinct response contract because bounded history intentionally lacks
  full plans/snapshots. The owner declined this cost and chose visible conflict;
  retaining full plans or adding a start marker/client gate is unnecessary.
- Retry in CLI or automatically after failure: hides acceptance and can duplicate
  work or loop failures; no retry engine is required for one explicit attempt.
- Invoke restart unconditionally: reuses code but would replace healthy runtime,
  lose idempotency and risk inheriting image consent semantics into ordinary start.
- Broaden only the allowed enum list: small diff but skips stale-owner joining,
  current configuration adoption, protected drift and attachment authority checks.
- Reset failed resources: may make launch succeed but destroys retained application
  data/credentials and contradicts the explicit recovery boundary.

## Revisit when

Users require automatic service restart, transparent daemon upgrade continuation,
exact historical start responses, or explicit durable request deduplication. Those
require a separate owner decision and failure/retention analysis; this change
supplies none of them.

## References

- [Issue 99](https://github.com/octacian/backlot/issues/99)
- [Lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Recorded targeting](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Attempt-bound attachment](20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md)
- [Startup disclosure](20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
- [Image-pin recovery](20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md)
- [Inherited private configuration](20261021T001732173Z-register-private-project-configuration.md)
- [Coordinator](../../internal/daemon/runtime.go), [transactional acceptance/recovery](../../internal/daemon/runtime_state.go)
- [Compatibility and destroy](../../internal/daemon/resources.go), [image pins](../../internal/daemon/build_images.go)
