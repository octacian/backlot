---
title: 'Ensure persistent start accepts one eligible current attempt'
date: '2026-10-28'
status: superseded
summary: 'Make explicit persistent start join running work or accept one compatible new attempt after prior work is reconciled without resetting identity or data.'
tags: [daemon, lifecycle, identity, cli, attachment]
supersedes: [20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md, 20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md, 20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md, 20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md, 20261021T001732173Z-register-private-project-configuration.md, 20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md]
superseded_by: [20261103T174043965Z-reassociate-retained-projects-through-explicit-current-bindings.md]
---

# Ensure persistent start accepts one eligible current attempt

Persistent start/run leaves starting or ready work unchanged and reports unapplied
configuration changes. Otherwise, explicit start can launch one compatible new
attempt after reconciliation, preserving identity, data and paired credentials.
Nothing retries or resumes after a crash automatically.

## Context

After repairing prerequisites, users need start to make one attempt without
requiring restart. Reusing stale failed work prevents recovery; unconditional
restart would replace healthy work. Overlapping requests must not multiply attempts.

## Decision

Finish preparation, cleanup and reconciliation through existing daemon coordination;
wait for prior owners/collectors before accepting a new attempt from prepared,
stopped, failed, cancelled or interrupted state. Uncertain cleanup or ownership
blocks replacement; collection failure alone does not. Recovery records interruption
without launching. Named start after destroy creates a new ID; selecting an old ID
never redirects.

Capture the instance, observed attempt/status and latest acceptance sequence together.
Keep each request's selection while waiting for the mutation lock; atomically recheck
revision and eligibility when accepting. If two callers observe A and one starts its
immediate successor B, the queued ordinary caller returns B while current, even if
finished or its history was pruned. If C replaces B, return typed `startup_conflict`
with history guidance; do not start D. Missing facts return unavailability/conflict,
never a different attempt or invented historical plan. Callers observing starting/ready
work stay bound to it after termination. Only a later explicit call observing eligible
terminal work may start again.

CLI polling follows only the returned attempt. Replacement returns conflict with
its last validated snapshot labelled last observed. Startup cancellation supplies
`StopRequest.ExpectedAttempt`, checked before stop effects to protect replacements.
A missing mutation reply leaves acceptance/completion unknown; never retry automatically.

A new attempt uses configuration resolved once from the explicit machine path or
normal default, without inheriting recorded `ConfigPath`. Verify source identity;
rejected starts preserve prior launch provenance. Existing compatibility checks
protect fresh-only initialization, resource/route identity, shared allocations,
private provenance and image pins. Protected changes require explicit reset/destroy.
Reusing the same image remains valid; image-change and mounted-data consent require
explicit restart. Start grants neither.

Only the caller accepting a new attempt owns its attachment. Attached callers
refuse another caller's attempt; ordinary joins gain or renew no attachment authority.
Client request, cancellation, bounded cleanup and attachment renewal/expiry policies
remain unchanged.

This supersedes only the rules for starting unhealthy/recovered scenes and selecting
stop/start inputs in the records listed in `supersedes`. Their remaining startup
disclosure, privacy, supervision, ownership and lease contracts continue. Disposable
execution, restart/reset and original terminal exits are unchanged. Detailed operational rules
remain in [ordinary start](../cli.md#commands-and-defaults),
[attachment](../daemon.md#attached-execution) and
[retained-resource compatibility](../containers.md#generations-jobs-and-drift).

## Consequences

New attempts use compatible current configuration; running work keeps recorded
configuration. Existing acceptance/history transactions preserve prior attempts for
diagnosis. No new persistent state, migration, dependency, background worker or retry
engine is introduced. Replacement returns conflict; retained history supports diagnosis.

## Alternatives considered

- Always require restart: simpler, but makes recovery require a separate command.
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
