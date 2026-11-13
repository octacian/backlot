---
title: "Compose infrastructure controls with verified shutdown completion"
date: 2026-11-12
status: accepted
implementation: in-progress
summary: "Compose explicit infrastructure controls from existing daemon and gateway lifecycles, and distinguish successful cleanup plus ownership release from shutdown acknowledgment."
tags: [cli, api, lifecycle, gateway]
supersedes: []
superseded_by: []
related: [/docs/adr/20261028T235219299Z-install-verified-releases-with-bounded-daemon-maintenance.md, /docs/adr/20261111T214921188Z-stream-workload-wide-stop-outcomes-without-a-fixed-scope-ceiling.md, /docs/adr/20261027T183153280Z-use-framework-parsing-and-preserve-truthful-cli-results.md]
tracking: [https://github.com/octacian/backlot/issues/97]
implemented_by: []
---

# Compose infrastructure controls with verified shutdown completion

## Context and Problem Statement

Developers need one explicit way to start and stop the selected Backlot
infrastructure. Separate daemon and managed-gateway controls expose ordering and
shutdown observation details. Existing daemon shutdown already fences admission,
joins workloads, recovers ownerless cleanup and stops administrative resources;
its acknowledgment precedes final socket, database and lock teardown. The reported
restart-to-stop-gateway workaround needs reproduction, not another cleanup engine.

## Considered Options

- Keep separate controls and document their sequence: preserves the observation
  gap and requires callers to coordinate ordinary infrastructure use.
- Compose existing lifecycle operations with an explicit completion check: adds
  a small orchestration boundary while preserving cleanup authority and policy.
- Apply a fixed or workload-count-derived total deadline: bounds the observation
  but can reject legitimate cleanup as scope, configured grace and provider
  latency vary; counting workloads does not predict those costs reliably.
- Start administration automatically on reads, or add a persistent supervisor
  and shutdown journal: adds implicit effects and recovery obligations beyond
  this explicit workflow.

## Decision Outcome

Add `infra start`, `infra status` and `infra stop`, scoped to exactly one
default or explicit state directory. Start obtains compatible daemon access,
then starts the selected configured managed gateway through its existing
lifecycle and listener-readiness checks. Retained configuration remains usable;
explicit conflicting configuration fails rather than taking over or silently
reconfiguring resources. Missing managed configuration permits daemon-only use.
External gateways stay operator-owned. Host-only operation does not require
Docker, and infrastructure readiness does not establish host or application trust.
If gateway startup fails, report the available daemon and gateway outcome without
automatically rolling back the daemon.

Status combines compatible daemon access, managed-gateway readiness and bounded
recorded blocker diagnostics. Distinguish live probes, recorded intent and unknown
outcomes; incomplete activity pages cannot prove absence. Preserve selected scope
and actionable activity/inspection commands in human and typed results. Reads
do not start administration, recover resources or open the state database outside
its daemon owner.

`infra stop` performs cleanup by default without a prompt, cleanup flag or force
bypass. Reuse the daemon's permanent admission fence, owner joins, exact-resource
cleanup and retention policy, including active and kept workloads, routes and
owned dependencies. Preserve data, credentials, CA identity and history. Keep
`daemon stop`'s current command and wire semantics. Keep `gateway stop` gateway-only,
adding guidance to inspect activity or use `infra stop` when workloads block it.
Any demonstrated shared-cleanup defect is fixed in the existing lifecycle.

The server owns one shutdown coordination and terminal cleanup result, outside
the mutation-owner set it cancels and joins. Legacy stop and infrastructure
observation consume that same result; server teardown does not run cleanup a
second time. Reuse the bulk-stop transient buffering and independent writer,
without registering shutdown as a mutation that would join itself.

Stream compact typed acceptance, phase and per-workload progress/outcomes from
the same shutdown coordinator, rendered live for human callers too. Keep cleanup
independent of slow or disconnected output, reusing the bulk-stop observation
pattern without introducing a general event framework. Report independent phase
failures together. The daemon's cleanup result precedes its final teardown; the
foreground completion record follows the ownership check. Missing either required
result means completion is unknown.
Successful shutdown requires a successful cleanup
result followed by acquiring the existing state lock and verifying socket removal
and stable private path identities. Hold ownership through the final observation;
release it on return. This proves relinquished daemon ownership after cleanup,
not a universal claim about descendants or a permanent ban on later explicit
starts. Keep the lock inode and never infer cleanup authority from PIDs, process
liveness, transport loss or a public activity snapshot.

Do not impose a fixed or workload-derived aggregate shutdown deadline. Preserve
existing per-operation provider, configured stop-grace and owner-join safeguards
without tightening them. The live feed explains which workloads or phase remain
in progress. An unjoinable owner returns a cleanup failure to observers without
waiting for a success-only ownership proof. Correct the current process-level
retention gap: the serving process stays alive until remaining owners join,
retaining storage and the state lock, rather than returning with only a waiter
goroutine that process exit would discard. No deadline permits closing an owner's
storage or assuming its resources absent. Cancellation or a lost reply ends
observation with completion unknown; accepted daemon-owned cleanup may continue. Never retry the mutation
automatically or restart the daemon just to complete a normal shutdown. An
already absent daemon can be reported as absent, but absence alone does not prove retained resources were cleaned; unknown
residuals stay explicit. No background daemon means no independent database read
or provider cleanup by the CLI.

### Consequences

The additional API/result contracts and foreground orchestration reuse the
existing daemon, gateway, activity and local-lock implementations. Existing
lifecycle journals remain the sole durable resource authority: no new persisted
format, migration, durable operation receipt, dependency or background service is
introduced. Unused infrastructure commands add no running work. Completion
observation needs foreground waiting and brief lock ownership, while accepted
cleanup retains the daemon's existing owners and shutdown ordering.

Duration and compact transient progress storage may grow with the selected scope,
as with workload-wide stop. There is no promise to finish within two minutes;
operators can cancel observation without abandoning accepted cleanup. Report a
replacement daemon generation when observed and never stop it implicitly.
Successful original cleanup plus the subsequent held ownership check establishes
completion; transient evidence cannot detect every intervening daemon that starts
and exits before that check. Detecting all such generations would require a
stronger handoff or durable evidence beyond this workflow.

A disconnected caller cannot recover the original operation's successful result
from a missing socket. Existing activity and inspection remain the diagnostic
path while administration is available; recovery after abnormal daemon loss
remains explicit. Supporting resumable proof after a lost reply would require a
separate justified durable contract. The installer still needs to retain state
ownership through executable replacement; infrastructure completion does not
replace that requirement.
