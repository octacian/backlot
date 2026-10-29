---
title: 'Ensure persistent start accepts one eligible current attempt'
date: '2026-10-28'
status: proposed
summary: 'Make explicit persistent start join running work or accept one compatible new attempt after prior work is reconciled without resetting identity or data.'
tags: [daemon, lifecycle, identity, cli, attachment]
supersedes: []
superseded_by: []
---

# Ensure persistent start accepts one eligible current attempt

Make `scene start NAME` and persistent `run NAME` ensure a running scene through
one daemon coordinator: join an accepted start, return ready with unapplied drift,
or accept one new eligible attempt using compatible current inputs. Preserve
persistent identity, data and paired credentials, reconcile/join old work first,
and block uncertain ownership. One explicit invocation requests one attempt;
there is no background retry or crash continuation.

This is phase two of [issue 99](https://github.com/octacian/backlot/issues/99),
after delivery/review of [bounded attempt history](20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md).
Upon acceptance it will partially supersede the lifetime decision's requirement
for explicit restart after interrupted work, recorded targeting's unhealthy-start
rule and attachment's inherited failure/restart eligibility. It also replaces
startup disclosure's stopped/start snapshot-selection rule and the image-pin
recovery requirement for explicit restart, and overrides the inherited private
configuration restriction requiring restart for failed/interrupted instances.
Startup disclosure authorization, redaction and exact-attempt readiness polling,
image acceptance/consent, native supervision, private registry/privacy and all
unaffected identity, targeting, ownership, lease and cleanup rules continue by
reference. Keep metadata unchanged while proposed. On acceptance, add reciprocal
supersession links for lifetime, recorded targeting, attachment and startup
disclosure; append this successor on the already-superseded private-configuration
record and on the image-pin record superseded by phase one, preserving existing
successors. Phase one must not activate this decision early.

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
current-input validation, retain the selected predecessor and its acceptance
revision across serialization, then re-read the selected instance under the lock.
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

Concurrent ordinary invocations are bounded by their pre-lock observation, not
by whether the current attempt is still starting when they acquire the lock.
Retain the observed predecessor/acceptance revision (including no accepted attempt)
and compare it at transactional acceptance. If another acceptance overtakes that
observation, return/join the first successor accepted after that revision, even if
it has already failed, cancelled or completed cleanup. A caller that observed an
already-starting attempt remains bound to that attempt if it becomes terminal
while queued. Never reinterpret either case as permission to accept another
attempt. Phase one's per-instance acceptance order and exact-attempt records can
identify that successor; no durable request journal or network deduplication is
needed. If the bound result has been pruned or destroyed, report it unavailable
rather than substituting current work or launching again.

A genuinely later invocation observing an eligible terminal attempt can request
its successor after reconciliation. Preserve missing mutation reply as unknown
acceptance, with observation/diagnosis rather than automatic retry. Attached
requests retain the stricter rule below: being overtaken by any other acceptance
is refusal, never permission to join/adopt even a now-terminal attempt.

For a newly accepted attempt, resolve current compatible configuration and run the
existing retained-resource, private provenance, initialization, route identity,
shared allocation and image-pin checks. Atomically install the accepted snapshot
with the new attempt, so rejected acceptance cannot publish new launch provenance.
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
acceptance, and responses expose the exact new attempt. No copying old terminal
exits or collection failures into a new attempt. Durable native-waiver warnings
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
worker, timer, dependency, durable retry state or independent cleanup authority.
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
second caller, and prove both receive B with no C accepted. Also cover a caller
observing B starting before it becomes terminal, a later invocation accepting C,
and refusal of an overtaken attached caller. Existing provider/native confidence
limits remain unchanged.

## Alternatives considered

- Keep explicit restart for every unhealthy attempt: smallest code but preserves
  the reported operator trap and fails the approved ensure-running behavior.
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
or explicit durable request deduplication. Those require a separate owner decision
and failure/retention analysis; this change supplies none of them.

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
