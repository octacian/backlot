---
title: 'Supervise cooperative native process groups with durable guardian identity'
date: '2026-10-08'
status: superseded
implementation: implemented
summary: 'Require native descendants to stay in a dedicated supervised group and retain a durable guardian identity so cleanup can fail safely on uncertain ownership.'
tags: [native, lifecycle, ownership, deadlines]
supersedes: []
superseded_by: [/docs/adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
related: []
tracking: []
implemented_by: ["https://github.com/octacian/backlot/commit/b0419468fcdae3f28c80a5c9e6d96a16514ae293"]
---

# Supervise cooperative native process groups with durable guardian identity

## Context and Problem Statement

Native cleanup must not signal unrelated processes after PID reuse. macOS process
events and finite descendant enumeration cannot reliably discover every detaching
child; names, environment markers and open ports do not establish authority.

## Considered Options

- A cooperative dedicated group with an outside guardian and unreaped anchor.
- Leader-only or observed-PID cleanup: loses descendants or races identity reuse.
- Arbitrary descendant tracking: cannot eliminate detachment/enumeration races.
- Privileged Endpoint Security supervision: changes deployment and entitlement
  obligations beyond the chosen cooperative workload boundary.

## Decision Outcome

Require native commands and their descendants to remain in the dedicated workload
process group. An outside guardian retains a direct anchor child, pinning the group
identifier until all group actuation ends. This is a cooperation contract, not
hostile-workload containment.

Persist private launch identity and random control capability before application
activation. Cleanup uses authenticated guardian authority; never signal an observed
PID/PGID as fallback. Retain the anchor through bounded termination, then join
collectors and guardian and verify group absence before settling cleanup.
Missing/mismatched authority, incomplete descendants or collection retain failure
and uncertainty. Root exit, EOF and closed listeners alone prove neither cleanup
nor collection.

Keep original application exits independent of cancellation, collection and cleanup
outcomes. Runtime recovery interrupts rather than resumes applications. Execution,
termination and collection budgets have explicit ownership and bounds; no automatic
application restart/retry is introduced.

### Consequences

Cooperative commands gain identity-safe cleanup without privileged installation,
while detaching or externally supervised workloads remain unsupported. Guardian
loss can require manual recovery. Listener readiness needs ownership evidence,
not merely a successful network probe. Later decisions broaden application groups
and recovery while retaining stricter system-helper supervision.

## More Information

[Native execution](/docs/daemon.md#native-execution),
[Apple process events](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/event.h).
