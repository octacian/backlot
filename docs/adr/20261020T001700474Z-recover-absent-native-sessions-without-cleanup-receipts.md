---
title: 'Recover native ownership with boot provenance and explicit waivers'
date: '2026-10-20'
status: accepted
summary: 'Use boot-scoped native launch records and explicit force-stop waivers while keeping ownership, collection and original exits independent.'
tags: [native, gateway, recovery, ownership]
supersedes: [20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md, 20261016T201500000Z-support-explicit-linux-managed-https-reachability.md]
superseded_by: []
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

An unexpected power cycle left an installed gateway with unavailable guardian
control/receipt and retained CA/configuration. Empty listeners do not prove process
absence or successful collection. Existing bootless records cannot retrospectively
prove that a reboot occurred; prevention for future boots alone does not resolve
them. Root, daemon, guardian and combined guardian/anchor failures have different
cleanup authority, and resource release must not manufacture application success.

Global process censuses do not establish the required closed-session proof: kernel
enumeration can omit an exited parent and a newly forked child within one call;
equal returned lists cannot detect that lineage. Unrelated churn also denied bounded
macOS observations. Reject that fallback rather than weakening observation errors
or extending retry deadlines. The approved exceptional boundary uses an explicit
operator acknowledgement instead of another inference subsystem.

## Decision

Persist valid kernel boot identity before every native launch intent, including
scene jobs/services, command probes and the managed gateway; bind completed effects
to that same boot. Failure to obtain provenance fails before allocation. A valid
boot mismatch releases only its obsolete native obligations and pre-effect intents.
Missing/invalid identities stay unknown; never invent provenance for legacy records.
Keep route, container, configuration, CA, permission and trust checks independent.

Keep authenticated cleanup first. Distinguish authenticated collector completion
from resource-only absence inside native lifecycle code. Only the former attests
collection. Resource-only cleanup, authority loss and force waiver retain sticky
collection gaps even when an original root exit is already known. Keep that code
independently; normal EOF after authenticated completion must remain successful.

Keep the private guardian-only writer pipe to a live self-pinned anchor. On EOF
that anchor terminates its own group; daemon loss must not close the pipe. This is
best-effort cooperative cleanup, not collector proof or restart eligibility.
Anchor-only death remains cleanable by its guardian; simultaneous authority loss
may require force or controlled reboot. No observed PID/PGID authorizes actuation.

Force is supported only on the two existing stop commands, never start/restart,
reset or destroy. Serialize the selected target, first attempt normal cleanup and
join its finite execution owner, and retain non-native cleanup fences. Commit exact
unresolved native identities or explicitly unidentified intent, prior attempt/result,
time, acknowledgement and available boot provenance atomically with relinquishment.
Failure/cancellation before commit cannot release a claim; retry cannot waive a
later attempt. Close/join local status readers without signaling abandoned owners.
Retain direct-child reaping ownership until accepted survivors exit.

Store narrow private histories on existing instance and managed gateway records,
outside replaced attempt journals. Retain them across restart, daemon recovery and
evidence pruning for the target's retained lifetime. Expose only safe typed warnings
in responses/status/logs/export, excluding capabilities and private paths. Force may
report acknowledgement applied, never verified cleanup. The abandoned attempt stays
incomplete; a fully collected replacement can be complete with the prior-ownership
warning retained. Gateway-only waiver does not invent a scene result.

State format 6 fences older readers before waiver. Permit only a narrow additive
transition from the complete supported v5 layout, preserving records, keys and CA/
configuration files. Reject other versions or corrupt layouts unchanged. No state
reset or general migration framework is introduced.

A later ordinary reset/destroy or other data removal must independently prove that
waived native consumers cannot remain, through trusted later-boot provenance or
restored authenticated authority. Waiver/gaps are not deletion permission. Preserve
history even when subsequent independent proof permits deletion. Force cannot
bypass exact routes, containers, active/kept published scenes, configuration drift,
CA identity, paths, trust, ports or ordinary startup readiness. Explicit gateway
start resumes validated saved effective configuration rather than stale bootstrap.

## Consequences

Recovery can support installed bootless/missing-effect cases without guessing
ownership or resetting state. Explicit force accepts duplicate work and concurrent
writes; a free port cannot rule those risks out. Ordinary stop preserves unknowns,
and operators can instead restore authority or perform a controlled reboot.

All native writers persist boot provenance. Private cleanup proof, typed stop
requests, retained per-target histories, safe warnings and destructive guards add
bounded lifecycle work to existing owners/storage; the pipe adds one descriptor
pair per workload. There is no periodic scan, public rescue service, new dependency,
privileged containment, trust operation, PGID handshake or automatic restart.
Actual-host platform and final-candidate acceptance remain separate from design
acceptance; container evidence is not supported Linux-host acceptance.

## Alternatives considered

Authenticated cleanup alone preserves the installed failure indefinitely. Boot-only
prevention is smaller but cannot resolve old bootless claims. Double global census
has insufficient completeness and availability justification. Persisting new group
boundaries before root activation could support stronger automatic recovery but
requires a new handshake and cannot retrofit old effects; unforced guardian-loss
restart was not requested. More durable receipts/control paths reduce file loss
but cannot attest collection after abrupt death.

Requiring reboot attestation for every waiver reduces live-survivor risk but fails
the selected explicit no-reboot boundary. The owner instead accepts that risk through
force. Stronger launchd/systemd/cgroup containment or redundant authorities would add
unjustified deployment obligations. State reset, fabricated receipts, process-name
or port kills and observed-ID actuation violate retained guarantees.

## Revisit when

Unforced restart after lost authority, detached workloads, unknown-survivor recovery
without explicit risk acknowledgement, destructive cleanup without independent
proof, or stronger containment becomes a concrete requirement. Revisit if actual
boot/platform evidence contradicts the provenance and retained-state assumptions.

## References

- [Native supervision decision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Original managed gateway decision](20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md)
- [Current accepted gateway decision](20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
- [Explicit scene lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Native implementation](../../internal/native/authority.go)
- [Scene recovery](../../internal/daemon/runtime_state.go)
- [Managed gateway lifecycle](../../internal/daemon/managed_gateway.go)
- [Recovery operations](../https.md)
