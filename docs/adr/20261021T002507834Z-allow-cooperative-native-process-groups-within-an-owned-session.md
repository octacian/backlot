---
title: 'Allow cooperative native process groups within an owned session'
date: '2026-10-21'
status: accepted
summary: 'Allow ordinary native watchers to create process groups inside a live guardian-owned session, with bounded identity-safe cleanup and explicit residual-process risk.'
tags: [native, lifecycle, ownership, watchers, macos, linux, implemented]
supersedes: [20261021T001732173Z-register-private-project-configuration.md]
superseded_by: []
---

# Allow cooperative native process groups within an owned session

Application native services, jobs and readiness commands may create process groups
within their live guardian-owned Unix session. Backlot uses kernel process
identities for bounded cleanup and reports successful session cleanup as
**observed empty**, with an explicit risk of unseen survivors. Backlot-managed
system helpers, including stock Caddy, retain strict pinned-group supervision.
Ordinary Air and `npm run dev` commands need no watcher-specific adapter.

This decision changes only native supervision and the associated state transition
from the [private configuration decision](20261021T001732173Z-register-private-project-configuration.md).
Its configuration, registry, privacy and CI contracts remain in force, as do the
existing scene-targeting, boot-recovery, force-acknowledgement and evidence rules.
The implementation is part of [PR #61](https://github.com/octacian/backlot/pull/61).

## Context

[Issue #49](https://github.com/octacian/backlot/issues/49) demonstrates that Air
launches a rebuilt application in a different process group. The previous listener
check rejects that application because cleanup reaches only the original group.
Accepting its listener without extending cleanup leaves a known child outside the
supervision boundary.

A session accommodates ordinary child-group watchers without changing application
commands, adding a privileged supervisor or maintaining a recursive process graph.
However, finite enumeration cannot prove complete session absence: a fork can race
the final observation. Kernel identities prevent signalling a replacement process;
they do not make enumeration complete. The chosen tradeoff preserves identity-safe
actuation and truthful uncertainty while accepting a weaker absence guarantee.

## Decision

### Ownership and identity

The live authenticated guardian leads the application session and owns a retained
anchor in the original workload group. Application descendants remain in that
session with ordinary same-user signal permissions. New sessions, privilege
changes, external supervisors and hostile escape are unsupported. This is a
cooperative execution contract, not containment.

Listener validation and cleanup use authenticated guardian authority. For each
candidate, the guardian acquires a kernel identity, observes session membership,
and revalidates identity before actuation. A disappearing or replaced listener is
not ready; it does not authorize signalling an unrelated process. A saved PID,
process-group ID, session ID, name or environment marker is never cleanup authority.
There is no fallback from failed identity validation to numeric-PID signalling.

Linux uses pidfds; macOS uses audit-token signalling through pinned purego bindings.
Purego preserves CGO-disabled builds without handwritten runtime linkage, at the
cost of dynamic system-library bindings and ABI maintenance. Platform capability
failures are explicit; Backlot does not silently downgrade application supervision.
Container-only and offline work do not load the Darwin bindings or scan processes.

### Bounded cleanup and confidence

The guardian performs bounded TERM/grace/KILL scans. Known live identities remain
cleanup obligations even if a later enumeration misses them. The retained anchor
outlives session scans; its original group retains the strict kernel absence check.
Required process, status and output owners join, and an authenticated receipt
binds the cleanup observation to the original authority and execution attempt.
Original job exit, collection failure and cleanup failure remain separate facts.

Two complete, separated observations with no live workload members produce
`observed_empty`. This is successful cleanup under the accepted residual-process
risk, not proof of universal descendant absence. An unseen fork or unsupported
escape can survive a successful stop and race restart or owned-data deletion.
Ordinary lifecycle, output and data fences may be released under this tradeoff.
Positively known live members, denied observations, exhausted budgets, missing
joins and lost authority remain incomplete, nonzero and fenced.

The shared per-component, per-attempt cleanup result distinguishes `observed_empty`,
`incomplete` and `authority_lost`, with bounded non-secret reasons. Human output,
JSON, Doctor and exports expose the same limitation. Explicit acknowledgement of
risk is a separate operator decision, never an observation of successful cleanup.
System helpers retain their stronger original-group policy and do not acquire
application-session confidence merely because they share supervisor code.

### Recovery and compatibility

A daemon replacement reuses the surviving guardian's authenticated authority;
it does not resume the application or invent missing output. A valid bound receipt
attests only its recorded observation. Guardian loss or mismatched authority does
not authorize scans against a saved session ID. Affected instance, output and data
fences retain uncertainty while unrelated daemon operations remain available.
Recovery is finite, without a new polling service or indefinite retry loop.

Existing earlier-boot proofs and exact-attempt force acknowledgements remain
bounded ways to release eligible native claims. They preserve historical cleanup
confidence, collection gaps and original exits, and do not bypass independent
non-native or abandoned-data guards. Root exit, EOF and closed listeners alone
never establish cleanup. Witness-based guardian-loss recovery and a second cleanup
authority are outside this decision.

State version 9 supports one-way upgrades from merged-main versions 6 and 8.
Under exclusive ownership, validation and the version fence share one transaction;
only the fence changes. Retained records, passive metadata, history, configuration,
CA material, data and registry provenance remain intact. Unsupported, corrupt or
incompatible experimental state is rejected unchanged with matching-binary guidance.
Precommit failure rolls back; a later startup failure leaves the committed version
in place. Upgrade does not provide live handover, automatic workload restart,
reset or a general migration chain. Current procedures and wire compatibility
are documented in the [state compatibility guide](../containers.md#state-compatibility).

## Consequences

Ordinary development watchers work without replacement tooling, privileged
installation or a permanent process tracker. The same policy applies to cooperative
native applications rather than a list of supported executable names.

Successful application cleanup has weaker absence confidence than pinned-group
cleanup. Unseen survivors remain possible, and lost authority can require explicit
operator recovery. Platform identity bindings, listener validation, durable results
and compatibility handling carry maintenance cost. Keeping these limits explicit
avoids promising complete descendant supervision that the mechanism cannot supply.

## Alternatives considered

- **Keep strict groups and change watcher tooling.** Cooperative Watchexec
  configuration retains stronger group cleanup with less Backlot code, but requires
  replacing normal Air workflows. General session support better fits the chosen
  developer experience.
- **Accept session listeners but clean only the original group.** This knowingly
  leaves ordinary watcher children beyond cleanup reach.
- **Track ancestry, names or environment markers.** These observations neither
  establish safe actuation authority after PID reuse nor cover every fork.
- **Add witness recovery, a surviving keeper or privileged containment.** These
  mechanisms add authority, persistence and lifecycle obligations. Witnesses and
  keepers still have owner-loss boundaries; stronger containment changes the
  deployment model. Their cost is not justified by the current cooperative need.
- **Require cgo for macOS bindings.** Conventional bindings change the shipped
  CGO-disabled build contract. Purego keeps that contract with a small OS adapter;
  binding failures or maintenance costs can justify revisiting the choice.

## Revisit when

Unseen survivors occur in ordinary supported watchers; authority loss repeatedly
leaves unusable instances or data; supported platform APIs cannot provide
identity-safe actuation; purego or runtime upgrades break the bindings; or adopters
need new sessions, changed privileges or hostile containment. Stronger recovery or
absence promises require a new guarantee and lifecycle-cost decision.

## References

- [Issue #49](https://github.com/octacian/backlot/issues/49) and [implementation PR #61](https://github.com/octacian/backlot/pull/61)
- [Original native supervision rationale](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Native execution and recovery](../daemon.md#native-execution)
- [Platform policy and dependencies](../platforms.md)
- [Testing guidance](../testing.md#native-watcher-example-and-browser-hmr)
- [Native authority implementation](../../internal/native/authority.go)
- [Session cleanup and listener validation](../../internal/native/session_unix.go)
- [Kernel identity adapters](../../internal/native/procidentity/identity.go)
- [Runtime journal and recovery](../../internal/daemon/runtime_state.go)
