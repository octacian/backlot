---
title: 'Recover native ownership with boot provenance and explicit waivers'
date: '2026-10-20'
status: superseded
implementation: implemented
summary: 'Use boot-scoped native launch records and explicit force-stop waivers while keeping ownership, collection and original exits independent.'
tags: [native, gateway, recovery, ownership]
supersedes: [/docs/adr/20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md, /docs/adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md]
superseded_by: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261021T001732173Z-register-private-project-configuration.md]
related: []
tracking: []
implemented_by: ["https://github.com/octacian/backlot/commit/a17f9c907c5f2f912ac6a75d66d7b38edc75e874"]
---

# Recover native ownership with boot provenance and explicit waivers

## Context and Problem Statement

A reboot or lost guardian can leave retained native claims without authenticated
cleanup receipts. Free ports and process censuses cannot prove absence or
collection. Permanent refusal also prevents the required no-reboot recovery path.

## Considered Options

- Authenticated cleanup, boot provenance and explicit exact-target waivers.
- Authenticated cleanup alone: leaves lost-authority cases trapped indefinitely.
- Global descendant censuses: cannot prove completeness and fail under unrelated churn.
- Stronger supervisors/redundant authorities: add deployment obligations beyond
  the chosen cooperative boundary.
- State reset, fabricated receipts or observed-PID kills: discard truth or authority.

## Decision Outcome

Persist valid kernel boot provenance before native effects. A trusted boot mismatch
releases obsolete native obligations, never independent container/route/trust
checks. Missing or invalid provenance remains unknown. Try authenticated cleanup
first; only authenticated collector completion attests collection. Preserve known
original exits independently of sticky collection gaps.

Allow targeted `stop --force` and `gateway stop --force` to relinquish unresolved
native claims without signalling observed processes. Bind acknowledgement to the
exact attempt or gateway revision, serialize/recheck before commit, and never
transfer it to replacement work or silently infer it for retries/other operations.
Retain private waiver history and safe public warnings; report acknowledged
relinquishment rather than verified cleanup.

A force-stop waiver accepts surviving work and writes. Later data deletion needs
independent absence/authority proof, even after restart. Preserve non-native
fences, saved effective gateway configuration, CA identity and retained data.
Recovery never resumes applications. The original narrow state transition was
replaced by later compatibility decisions; boot/authority/waiver guarantees remain.

The [attempt-history decision](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)
replaces retention of resolved native-abandonment history after successful destroy.
Other privacy, ownership and recovery guarantees remain in force.

### Consequences

Explicit force plus ordinary start/restart enables bounded recovery without reboot,
but may permit duplicate work and concurrent writes. Lost authority never becomes
proof of successful collection. Operators may instead restore authority or reboot;
no periodic scanner, public rescue service or automatic restart is added.

## More Information

[Recovery operations](/docs/https.md), [native authority](/internal/native/authority.go).
