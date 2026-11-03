---
title: 'Ensure persistent start accepts one eligible current attempt'
date: '2026-10-28'
status: superseded
implementation: implemented
summary: 'Make explicit persistent start join running work or accept one compatible new attempt after prior work is reconciled without resetting identity or data.'
tags: [daemon, lifecycle, identity, cli, attachment]
supersedes: [/docs/adr/20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md, /docs/adr/20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md, /docs/adr/20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md, /docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md, /docs/adr/20261021T001732173Z-register-private-project-configuration.md, /docs/adr/20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md]
superseded_by: [/docs/adr/20261103T174043965Z-reassociate-retained-projects-through-explicit-current-bindings.md]
related: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md]
tracking: ["https://github.com/octacian/backlot/issues/99"]
implemented_by: ["https://github.com/octacian/backlot/commit/11fb43f01ff720b83bcba82c50226689eb17775c"]
---

# Ensure persistent start accepts one eligible current attempt

## Context and Problem Statement

After repairing prerequisites, users need start to make one compatible attempt.
Requiring restart for all failed work obstructs recovery; unconditional restart
replaces healthy work. Overlapping requests must not multiply attempts or acquire
authority over another caller's execution.

## Considered Options

- Join running work or accept one reconciled, compatible successor on explicit start.
- Always require restart: simpler, but makes ordinary recovery a separate command.
- Return historical success after replacement: needs archived plans or another response.
- Automatic retry or unconditional restart: duplicates attempts, replaces healthy
  work or risks inheriting image-change consent.
- Broader status checks or reset alone: bypass reconciliation or destroy retained data.

## Decision Outcome

Starting/ready work remains unchanged, with unapplied configuration changes reported.
Otherwise explicit persistent start/run may accept one new attempt after preparation,
reconciliation and prior owner/collector joins. Uncertain cleanup or ownership blocks
replacement; collection failure alone does not. Preserve logical identity, application
data and credentials. Recovery never launches automatically. Named start after destroy
creates a new ID; selecting a historical ID never redirects.

Capture observed instance, attempt/status and acceptance sequence together. Keep that
selection while waiting for mutation serialization and atomically recheck eligibility.
Queued callers observing A join its immediate successor B while current, even when
finished or its history was pruned. If C replaces B, return conflict rather than
start D. Callers observing starting/ready work remain bound to it after termination;
only a later explicit call observing eligible terminal work can start again.
Missing facts mean unavailability/conflict, never a substituted attempt or invented plan.

CLI polling follows only the returned attempt. Replacement yields conflict with the
last validated snapshot labelled last observed. Cancellation is fenced to the expected
attempt before stop effects. Missing mutation replies leave acceptance/completion
unknown and never authorize automatic retry.

Resolve current configuration once from the explicit machine path or normal default,
without inheriting recorded configuration paths. Verify source identity and preserve
prior provenance on rejection. Existing initialization, resource/route, shared
allocation, private-provenance and image-pin checks still apply. Protected changes
need explicit reset/destroy; image-change and mounted-data consent require explicit
restart, never ordinary start.

Only the caller accepting a new attempt owns its attachment. Attached callers refuse
another caller's attempt; ordinary joins acquire or renew no attachment authority.
Existing cancellation, bounded cleanup and renewal/expiry policies remain unchanged.

Replace only unhealthy/recovered start eligibility and stop/start input selection
in the superseded records. Preserve their disclosure, privacy, supervision, ownership
and lease guarantees. Disposable execution, restart/reset and original terminal
outcomes remain unchanged.

### Consequences

Healthy work keeps recorded configuration while eligible new attempts use compatible
current inputs. Existing acceptance/history transactions preserve predecessor diagnosis.
Replacement produces a visible conflict instead of historical success. No new state,
migration, dependency, worker or automatic retry engine is introduced. Seamless daemon
continuation or exact historical start responses would require a new decision.

## More Information

[Ordinary start](/docs/cli.md#commands-and-defaults),
[attachment](/docs/daemon.md#attached-execution),
[resource compatibility](/docs/containers.md#generations-jobs-and-drift).
