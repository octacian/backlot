---
title: 'Recover absent native sessions without cleanup receipts'
date: '2026-10-20'
status: proposed
summary: 'Use kernel boot identity and conservative session absence checks to recover lost control authority without signaling observed processes.'
tags: [native, gateway, recovery, ownership]
supersedes: []
superseded_by: []
---

# Recover absent native sessions without cleanup receipts

Extend native recovery to distinguish missing cleanup authority from resources
that demonstrably no longer exist. Record kernel boot-session identity before
managed gateway launch intent and with every guardian effect. After reboot,
release those obsolete process claims without generating cleanup receipts or
claiming complete output collection. Preserve saved configuration, scene history,
data, CA identity and trust, and require explicit workload/gateway startup.

Give the workload anchor a private guardian-liveness pipe. Guardian death causes
the still-live anchor to terminate its own pinned process group, reducing orphaned
workloads after SIGKILL. Recovery never signals an observed PID or process group.

## Context

An installed managed gateway became permanently uncertain after an unexpected
machine power cycle. Its guardian socket and receipt were unavailable, no Caddy
listeners were observed, and retained CA/configuration remained present. Temporary
control directory cleanup is a possible cause, not established evidence. The
existing authenticated-only cleanup rule could not release the obsolete claim.

Application/root exits, daemon exits and guardian exits are different failures.
A surviving guardian can clean up an application after root exit or daemon loss.
Previously, guardian death left the anchor and cooperative descendants alive with
no supported replacement cleanup authority. That makes a routine developer fault
unnecessarily difficult to recover from.

## Decision

Keep authenticated guardian cleanup as the normal path. Add the operating system's
boot UUID to private native identities, and record it before managed gateway launch
intent. Compare UUIDs, never wall-clock birth timestamps, to establish reboot.
Unknown or malformed boot identity does not authorize release. Existing optional
fields remain readable; no state reset or secret-bearing public contract is added.

When control is unavailable and the receipt is missing, establish original guardian
exit and inspect its entire session. All supported native descendants remain in
that session. Verify process birth identities around session observations; a
surviving member, disappearing/reused process, permission failure or unsupported
observation blocks release and permits a later retry. macOS uses kernel process
records; Linux requires an unfiltered procfs and rejects filtered/unreadable views.
Do not interpret empty ports, root exit alone, EOF or PID reuse alone as absence.
Malformed/mismatched receipts remain failures. Legacy launch intents without an
effect or boot identity remain uncertain because they identify no session.

The guardian alone holds the writer of the anchor's anonymous pipe. On EOF the
anchor verifies that its process group is its own PID and sends KILL to that
self-pinned group. The live anchor prevents group-ID reuse during this operation.
It never retries a signal after exit or group release. Ordinary cleanup retains
its existing TERM/grace/KILL, anchor join and collector proof sequence. Daemon
loss does not close the guardian-to-anchor pipe. Workloads do not inherit its
writer. Fault fixtures retain independent cleanup before killing a guardian.

Absence is resource cleanup evidence, not an original exit status or a collection
receipt. Interrupted scene recovery retains collection gaps; live owner loss
retains unavailable status and collection failures. Explicit restart may proceed
after resource absence, while history continues to report those gaps.

## Consequences

Reboots and vanished sessions can recover through existing daemon recovery and
explicit stop/start commands, preserving retained state. Guardian SIGKILL ends its
cooperative workload rather than leaving it indefinitely orphaned. Caddy still
resumes validated effective configuration and uses the retained CA.

A live legacy guardian/session with lost authority still blocks replacement;
this change cannot retrofit a liveness pipe into an already running anchor.
Killing both guardian and anchor independently while leaving descendants, detached
workloads, restricted process visibility, lost ownership effects and corrupt
journals still require diagnosis. There is no unconditional force-reset promise.
Session enumeration may conservatively fail during process churn; retrying is
safe. Boot UUIDs provide the simpler recovery path for new records after reboot.

## Alternatives considered

Persistent receipts alone cannot describe cleanup after abrupt power loss, and
long persistent socket paths exceed Unix socket limits. A private short control
directory remains appropriate for authenticated live operations.

Using process birth time versus wall-clock boot time is sensitive to clock changes.
Kernel boot UUIDs avoid that ambiguity. Boot UUIDs alone do not recover existing
records or same-boot guardian death, hence the session absence path and anchor pipe.

A force-forget switch or PID/name/port-based kill risks releasing live ownership
or affecting unrelated processes. A privileged supervisor or separate rescue
daemon adds a new deployment dependency and is disproportionate to this failure.

## Revisit when

Workloads must detach from their supervised session, OS process visibility cannot
support absence checks, or a privileged containment adapter is approved. Revisit
legacy intent recovery if an installation has an actual missing-effect case.

## References

- [Native supervision decision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Managed gateway decision](20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md)
- [Native implementation](../../internal/native/authority.go)
- [Managed gateway lifecycle](../../internal/daemon/managed_gateway.go)
- [Recovery operations](../https.md)
