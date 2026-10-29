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

Explicit persistent start/run joins starting/ready work or accepts one eligible
current-input attempt after reconciliation. Preserve identity, data and paired
credentials. Ready work stays unchanged, with truthful unapplied drift. There is
no automatic retry or crash continuation.

## Context

After repairing prerequisites, users need start to make one attempt without
requiring restart. Reusing stale failed work prevents recovery; unconditional
restart would replace healthy work. Overlapping requests must not multiply attempts.

## Decision

Use existing daemon coordination: join preparation/cleanup, reconcile and join old
owners/collectors, then accept from prepared, stopped, failed, cancelled or
interrupted state with compatible current inputs. Unresolved cleanup/ownership
blocks replacement; collection failure alone is not an ownership fence. Recovery
records interruption without launching. Named start after destroy creates a new
ID; selecting an old ID never redirects.

Capture instance, observed attempt/status and acceptance highwater together during
selection; keep this binding request-local across serialization. Recheck revision
and eligibility atomically with acceptance. If two callers observe A and one accepts
its first successor B, the queued ordinary caller returns coherent B while B remains
current, even terminal or with pruned history. If C has replaced B, return typed
`startup_conflict` with history guidance, accepting no D. Missing facts mean
unavailability/conflict, never current fallback or a fabricated historical plan.
An observed starting/ready attempt stays bound through termination. Only a genuinely
later explicit call observing eligible terminal work may accept another attempt.

CLI polling stays pinned to the returned attempt. Replacement returns conflict
with its last validated snapshot labelled last observed. Startup cancellation
sends `StopRequest.ExpectedAttempt` before effects, protecting replacements.
Missing mutation replies remain unknown acceptance/completion, never retry permission.

New acceptance uses one captured current configuration resolution: explicit machine
path or normal default, never ordinary inheritance of recorded ConfigPath. Preserve
source-identity verification; rejected acceptance keeps prior launch provenance.
Retain fresh-only initialization, resource/route identity, shared allocations,
private provenance and image pins under existing compatibility checks. Protected
drift requires explicit reset/destroy. Same-image reuse remains valid; changed-image
and mounted-data consent remain explicit restart decisions. Start grants neither.

Attachment belongs only to a newly accepted winner. Overtaken attached callers
refuse adoption; ordinary joins acquire or renew no attachment authority. Keep
existing cleanup-aware client request policy, caller cancellation and bounded
cleanup; attachment renewal/expiry timing stays unchanged.

This supersedes only unhealthy-start/recovery eligibility and stopped/start input
selection in the metadata-linked records. Their remaining startup disclosure,
privacy, supervision, ownership and lease contracts continue. Disposable execution,
restart/reset and original terminal exits are unchanged. Detailed operational rules
remain in [ordinary start](../cli.md#commands-and-defaults),
[attachment](../daemon.md#attached-execution) and
[retained-resource compatibility](../containers.md#generations-jobs-and-drift).

## Consequences

Start can apply compatible current inputs after terminal work while healthy joins
keep recorded configuration. Existing acceptance/history transactions preserve
predecessor diagnosis. No new persistent state, migration, dependency, background
worker or retry engine is introduced. Replacement can visibly conflict; retained
history supplies diagnosis rather than a successful historical start response.

## Alternatives considered

- Always require restart: simpler, but retains the operator recovery trap.
- Return historical success after replacement: requires a distinct response or
  archived plans; visible conflict meets the chosen guarantee at lower cost.
- Retry automatically or invoke restart unconditionally: duplicates attempts or
  replaces healthy work and risks inheriting image-change consent.
- Broaden status checks alone or reset resources: bypasses reconciliation and
  compatibility, or destroys retained data.

## Revisit when

Automatic restart, seamless daemon continuation or exact historical start responses
become required.

## References

- [Issue 99](https://github.com/octacian/backlot/issues/99)
- [Bounded history](20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)
- [Identity and lifecycle](../daemon.md#identity-and-lifecycle), [current configuration](../manifest.md#registered-private-project-configuration)
