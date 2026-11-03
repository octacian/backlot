---
title: 'Allow cooperative native process groups within an owned session'
date: '2026-10-21'
status: superseded
implementation: implemented
summary: 'Allow ordinary native watchers to create process groups inside a live guardian-owned session, with bounded identity-safe cleanup and explicit residual-process risk.'
tags: [native, lifecycle, ownership, watchers, macos, linux]
supersedes: [/docs/adr/20261021T001732173Z-register-private-project-configuration.md]
superseded_by: [/docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/49"]
implemented_by: ["https://github.com/octacian/backlot/commit/b041400c864a237b30e2d6e34435b615acd031bb"]
---

# Allow cooperative native process groups within an owned session

## Context and Problem Statement

Ordinary Air/npm watchers create child process groups. Accepting those listeners
while cleaning only the original group knowingly leaves work outside supervision.
Finite session enumeration cannot prove complete absence, even with safe process
identity: a fork may race the final observation.

## Considered Options

- Keep strict groups and change watcher tooling: stronger cleanup, but requires
  replacing ordinary development workflows.
- Accept session listeners with group-only cleanup: leaves known children behind.
- Cooperative session cleanup with kernel-pinned identity and explicit limits.
- Recursive trackers, witnesses or privileged containment: add authority and
  lifecycle obligations beyond the selected cooperative workflow.
- Require cgo: changes the existing CGO-disabled distribution contract.

## Decision Outcome

Allow application services/jobs/probes to create groups inside their live
guardian-owned session. New sessions, privilege changes, external supervision and
hostile escape remain unsupported. System helpers such as Caddy retain strict
pinned-group supervision; application flexibility does not weaken those helpers.

Authenticate guardian authority and acquire/revalidate kernel identity for listener
checks and actuation. Linux uses pidfds; macOS audit-token signalling uses purego
to retain CGO-disabled builds. Capability failure is explicit, never a numeric-PID
fallback or silent supervision downgrade.

Bound termination scans and join process/status/output owners. Known live identities
remain obligations even if a later enumeration misses them. Two complete separated
empty observations report `observed_empty`, accepting unseen-descendant risk rather
than claiming universal absence. Known survivors, denied observations, exhausted
budgets, missing joins or lost authority remain incomplete, nonzero and fenced.
Keep original exits, collection and cleanup outcomes independent and visible.

Recovery reuses exact surviving authenticated authority, never a saved session ID
as actuation permission. Boot proofs and exact-attempt force waivers retain their
separate meaning and abandoned-data guards. Preserve inherited project-configuration,
privacy and HTTPS contracts. Successor records replace only startup disclosure and
state-transition clauses, not this supervision choice.

### Consequences

Ordinary watchers work without privileged installation or a permanent process
tracker, at the cost of platform bindings and weaker absence confidence. Unseen
survivors may race restart or data deletion even after successful observed-empty
cleanup. Lost authority can still require operator recovery; stronger guarantees
need a new containment/recovery decision.

## More Information

[Native execution](/docs/daemon.md#native-execution),
[kernel identity adapters](/internal/native/procidentity/identity.go).
