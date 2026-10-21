---
title: 'Recover native ownership with boot provenance and explicit waivers'
date: '2026-10-20'
status: superseded
summary: 'Use boot-scoped native launch records and explicit force-stop waivers while keeping ownership, collection and original exits independent.'
tags: [native, gateway, recovery, ownership]
supersedes: [20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md, 20261016T201500000Z-support-explicit-linux-managed-https-reachability.md]
superseded_by: [20261021T001732173Z-register-private-project-configuration.md]
---

# Recover native ownership with boot provenance and explicit waivers

Recover native launch obligations through authenticated cleanup or trusted evidence
that their kernel boot ended. When neither is available, targeted `stop --force
INSTANCE` and `gateway stop --force` explicitly relinquish unresolved native claims
without signaling observed processes. The operator accepts that survivors may still
run or write. Preserve exact private waiver history, truthful collection gaps,
known original exits, retained data, saved configuration and CA identity.

Explicit forced stop followed by ordinary restart/start supplies the supported
single-guardian-failure recovery boundary without reboot. Unforced restart is not
guaranteed after lost authority. Retain all unaffected native supervision and Linux
managed HTTPS guarantees from the superseded decisions and their referenced chain.

## Context

An unexpected power cycle left an installed gateway without guardian control or
receipt but with retained CA/configuration. Empty listeners prove neither process
absence nor collection. Bootless records cannot retrospectively attest reboot.
Root, daemon, guardian and combined guardian/anchor failures have different cleanup
authority; releasing resources must not manufacture application success.

Global process censuses cannot prove a closed session: enumeration can omit an
exited parent and newly forked child within one call, even across equal lists.
Unrelated churn also denied bounded macOS observations. Rather than weaken errors
or extend deadlines, use explicit operator acknowledgement, not another inference
subsystem.

## Decision

**Boot provenance.** Persist valid kernel boot identity before every native launch
intent (scene jobs/services, command probes and managed gateway); bind effects to
that boot. Missing provenance fails before allocation. A valid mismatch releases
only obsolete native obligations and pre-effect intents. Missing/invalid identities
stay unknown, including legacy records. Route, container, configuration, CA,
permission and trust checks remain independent.

**Collection and authority.** Try authenticated cleanup first. Only authenticated
collector completion attests collection; resource-only absence, authority loss and
waiver retain sticky collection gaps alongside independently known original exits.
Normal EOF after authenticated completion remains successful. Keep the private
guardian-only writer pipe to a live self-pinned anchor: EOF terminates its own group,
while daemon loss must not close the pipe. This is best-effort cooperative cleanup,
not collector proof or restart eligibility. Anchor-only death remains cleanable by
its guardian; simultaneous authority loss may require force or controlled reboot.
No observed PID/PGID authorizes actuation.

**Targeted force stop.** Force applies only to the two stop commands, never
start/restart/reset/destroy. A fresh CLI inspects the selected attempt or safe
gateway claim revision; typed requests retain that precondition through retries.
Check it under serialization before any cleanup, including pending/legacy claims.
Managed launch rotates the revision; stop/waiver retains it. Attempt normal cleanup,
join its finite execution owner and retain non-native fences. Atomically commit
relinquishment with exact unresolved identities or explicitly unidentified intent,
prior attempt/result, time, acknowledgement and available boot provenance.
Check cancellation after writes and before storage commit (the linearization point):
failure/cancellation before commit cannot release claims; cancellation racing during
commit may observe an applied result. A retry cannot waive a later attempt. Close/join
local status readers without signaling abandoned owners; retain direct-child reaping
ownership until accepted survivors exit.

**History and evidence.** Keep narrow private histories on existing instance/gateway
records, outside replaced attempt journals, across restart, daemon recovery and
evidence pruning for the target's retained lifetime. Responses/status/logs/export
expose safe typed warnings, never capabilities or private paths. Report applied
acknowledgement, never verified cleanup. Abandoned attempts remain incomplete;
fully collected replacements can be complete with the prior warning. Gateway-only
waiver creates no scene result.

**Compatibility and deletion.** State format 6 fences older readers before waiver.
Allow only the narrow additive transition from complete supported v5, preserving
records, keys and CA/configuration files; reject other versions/corrupt layouts
unchanged. No state reset or general migration framework. Later reset/destroy or
other data removal requires independent proof that waived consumers cannot remain,
through trusted later-boot provenance or restored authenticated authority; retain
history even after proof permits deletion. Waivers/gaps cannot bypass exact routes,
containers, active/kept published scenes, configuration drift, CA identity, paths,
trust, ports or normal startup readiness. Explicit gateway start resumes validated
saved effective configuration, never stale bootstrap.

## Consequences

Installed bootless/missing-effect cases can recover without guessing ownership or
resetting state. Force accepts duplicate work and concurrent writes; a free port
cannot rule out either. Ordinary stop preserves unknowns; operators can instead
restore authority or perform a controlled reboot.

Boot provenance on every native writer, private cleanup proof, typed stop requests,
per-target histories, warnings and deletion guards add bounded work to existing
owners/storage; the pipe adds one descriptor pair per workload. There is no periodic
scan, public rescue service, new dependency, privileged containment, trust operation,
PGID handshake or automatic restart. Actual-host and final-candidate acceptance
remain separate from design acceptance; container evidence is not Linux-host proof.

## Alternatives considered

Authenticated cleanup alone preserves the installed failure indefinitely; boot-only
prevention cannot resolve old bootless claims. Double global census lacks justified
completeness and availability. Persisting group boundaries before root activation
could strengthen automatic recovery but needs a new handshake and cannot retrofit
old effects; unforced guardian-loss restart was not requested. More durable receipts
or control paths reduce file loss but cannot attest collection after abrupt death.

Reboot attestation for every waiver reduces survivor risk but fails the selected
no-reboot boundary; the owner accepts that risk through force. Stronger
launchd/systemd/cgroup containment or redundant authorities add unjustified deployment
obligations. State reset, fabricated receipts, process-name/port kills and observed-ID
actuation violate retained guarantees.

## Revisit when

Unforced restart after authority loss, detached workloads, recovery without explicit
survivor-risk acknowledgement, deletion without independent proof, or stronger
containment becomes required; or actual boot/platform evidence contradicts the
provenance and retained-state assumptions.

## References

- [Native supervision decision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Original managed gateway decision](20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md)
- [Previous Linux gateway decision](20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
- [Explicit scene lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Native implementation](../../internal/native/authority.go)
- [Scene recovery](../../internal/daemon/runtime_state.go)
- [Managed gateway lifecycle](../../internal/daemon/managed_gateway.go)
- [Recovery operations](../https.md)
