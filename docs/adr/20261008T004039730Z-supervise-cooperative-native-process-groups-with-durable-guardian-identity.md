---
title: 'Supervise cooperative native process groups with durable guardian identity'
date: '2026-10-08'
status: proposed
summary: 'Require native descendants to stay in a dedicated supervised group and retain a durable guardian identity so cleanup can fail safely on uncertain ownership.'
tags: [native, lifecycle, ownership, deadlines]
supersedes: []
superseded_by: []
---

# Supervise cooperative native process groups with durable guardian identity

Native commands, including readiness commands, and all their descendants must
remain in their Backlot process group. Calling `setsid`, moving to another group,
or delegating execution to an unrelated supervisor is unsupported. This explicit
launch contract was approved for the native slice; it is not kernel containment
and does not guarantee detection of deliberately escaped descendants.

Use an outside guardian that owns an unreaped workload-group anchor.
Journal the guardian birth identity and a private random control capability before
activation. Signal only the anchor's pinned group, retain the anchor until no
other member remains, and prove collection and guardian exit before success.
Preserve uncertain ownership with a nonzero diagnostic.

## Context

macOS ARM64 is the runtime acceptance platform. Its public kqueue process events
do not provide recursive fork enrollment: Apple's `sys/event.h` says NOTE_TRACK,
NOTE_TRACKERR and NOTE_CHILD are unsupported, and NOTE_FORK omits the child PID
from the delivered event. Enumerating arbitrary descendants cannot eliminate the
race where a child detaches and reparents between observations. PID/name/port
matching and removable environment markers do not establish ownership.

The approved restriction makes one dedicated process group the execution boundary.
The outside guardian retains a direct anchor child until after every group signal.
A live or unreaped child keeps its PID allocated, so another process cannot reuse
that group ID during actuation. Observing birth identity then signaling a PID is
not atomic and is not an acceptable actuation authority.

## Decision

Persist a launch intent, start an inactive outside guardian, and establish its
private authenticated control socket. Commit its PID, kernel birth identity,
control path and random capability before application activation. An inactive
helper receives control EOF on launch failure; if it cannot join, retain launch
uncertainty. No observation-based PID kill is a startup fallback.

On activation the guardian starts a separate anchor child in a distinct process
group. The guardian remains outside that group in the same session, starts the root
as another direct child joined to the anchor group, and waits for the root
independently to preserve its original exit status. It collects application streams
and never waits for the anchor concurrently with group signaling. It sends TERM
and then KILL to that pinned group only. Once the anchor is a zombie and the group
contains no other member, the guardian performs its sole anchor Wait. It must never
signal that group after releasing the anchor, including failed cleanup retries.
No enumerated member PID is an actuation target. Output and status collectors join
under finite budgets, then the guardian persists an authenticated receipt, replies,
and exits. A successful stop also verifies the original guardian process exited.
A reused observed guardian PID is never signaled. Final group absence also
requires the kernel to return ESRCH to a negative-PGID signal-0 query; enumeration
alone is insufficient. Existing, reused or inaccessible groups fail this proof
and must never cause post-release actuation.

Private control sockets and receipts use random mode-0700 `/tmp/blctl-*` directories
to stay within Unix socket path limits; socket/receipt files use mode 0600. The
private runtime journal retains their paths and random capabilities. Recovery
authenticates a cleanup request to the same authority and verifies its response
and exit. Missing/mismatched authority, incomplete descendants, lost collectors
or an unjoined helper fail cleanup and preserve uncertain ownership. Do not infer
absence from root exit, EOF or a closed listener. Recovery marks interrupted and
reports collection gaps; it never resumes or retries the application.

Use shared API duration strings, with defaults of 5 minutes for startup/dependency
work, 30 minutes per job, and 10 seconds of graceful termination. Execution/probe
budgets accept zero for unlimited and reject negative values. Another applicable
finite budget still bounds work. Graceful termination accepts any finite
nonnegative duration; zero means immediate escalation and verification/collection
remain separately bounded. Disposable leases remain finite even
when all execution budgets are unlimited. Native execution has no automatic
service restart or job retry; port allocation conflicts alone permit up to three
verified-cleanup allocation attempts.

## Consequences

Cooperative native commands have an explicit, testable cleanup boundary without
privileged installation. Existing watchers may manage children within that group.
Programs that daemonize, detach, or outsource work need a later supported adapter.
`lsof` is required for native TCP/HTTP listener ownership checks; a network probe
cannot accept an unrelated listener as readiness. Windows currently cross-builds
but does not execute native scenes.

A guardian crash can make surviving work uncertain; preserve it and return failure.
Retained status may report the original job exit independently of cancellation,
collection and cleanup failures. Output descriptors that remain open after root
exit are bounded by a collection deadline and reported as gaps, never success.

## Alternatives considered

Leader-only cancellation and process-group signaling without an ownership anchor
cannot safely reconcile root exits or PID reuse. Polling arbitrary descendants
and environment markers leave undetectable detachment races. Endpoint Security
requires Apple-granted entitlements and privileged operation, and its event-loss
and crash behavior would need a separate design. It is not introduced here.

## Revisit when

An adopting command requires detached descendants, malicious-workload isolation,
transparent daemon upgrades, or a privileged supervisor becomes an approved
product dependency. Linux/Windows runtime support requires its own acceptance
rather than inference from compilation.

## References

- [PRD execution contract](../prd.md#execution-and-dependencies)
- [Native daemon guide](../daemon.md#native-execution)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Apple XNU process-event header](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/event.h)
- [Apple Endpoint Security entitlement](https://developer.apple.com/documentation/BundleResources/Entitlements/com.apple.developer.endpoint-security.client)
