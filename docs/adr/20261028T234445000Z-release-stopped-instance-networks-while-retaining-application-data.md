---
title: 'Release stopped instance networks while retaining application data'
date: '2026-10-28'
status: accepted
summary: 'Release authenticated instance networks after verified consumer teardown and recreate them on later start to avoid idle address-pool consumption without deleting retained data.'
tags: [containers, networking, lifecycle, cleanup, recovery, compatibility]
supersedes: []
superseded_by: []
---

# Release stopped instance networks while retaining application data

Treat implicit and declared instance Docker networks as runtime resources: release
them after verified consumer teardown and recreate them on later start. Keep the
same instance and data generation, volumes, directories, generated credentials and
fresh-only completion records. Preserve recorded names for implicit and declared
networks, including supported `network.name` references; Engine IDs and subnets may
change. Container attachments resolve from the current allocation at each start.

## Context

[Issue 96](https://github.com/octacian/backlot/issues/96) reports address-pool
exhaustion and indiscriminate pruning as a workaround. An owned-fixture investigation
at commit `6efda8051396286a95903dd2189a4ce20073e1de` observed three distinct
persistent instances leave three empty networks after stop. Repeated starts of one
instance reused its network and retained a volume witness; ordinary disposable
success/failure and destroy removed their fixture networks. This establishes idle
persistent retention, not a general leak or the cause of every operator allocation
failure. Raw reproduction and cleanup evidence belong in the PR.

The existing [lifecycle decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
requires persistent data retention, not stable network IDs. The current container
guide additionally promises network retention; this proposal changes that contract.
Shared-service networks have separate project ownership and remain retained under
[their decision](20261019T050443877Z-add-project-scoped-shared-service-allocations.md).

## Decision

Release only networks recorded in the selected instance's resource generation,
including `@network` and declared networks. Apply the same rule to normal execution
teardown, explicit stop (including already stopped historical instances), stopping
kept work, daemon shutdown and existing interrupted-execution recovery. Never
release networks while investigation services remain kept. Metadata-only prepare
stays allocation-free; startup does not sweep unrelated stopped instances.

Join owned native/container consumers and preserve existing ownership, abandonment
and cleanup fences before release. Authenticate each recorded network with its
capability and exact acknowledged Engine ID; reconcile unacknowledged create effects
using the existing capability journal. Verify no Docker consumer remains, including
stopped foreign containers; Engine refusal is an additional protection against a
racing attachment. Never disconnect or remove a foreign consumer to free a network.
Unknown ownership, unavailable inspection, failed joins and known consumers retain
the network and return a cleanup failure through existing result fields.

Reuse the private resource journal and per-instance lifecycle serialization. Record
successful removal or authoritative absence using its existing removal state;
retain unresolved handles. Do not erase a network record before its effect is
settled. On a later accepted start, replace a released network allocation with
journaled create intent before Engine mutation, preserving its recorded name and
authenticated ownership authority, then persist the new effect before launching
attached containers. A foreign same-name collision fails startup; a name alone
never authorizes adoption, deletion or replacement. Reconcile interrupted removal and creation without
losing authority or silently replacing missing storage. Missing network effects
may be recreated only after authoritative absence; provider or ownership errors
are not absence. Existing volume/directory/secret verification and restart drift
checks, including Docker endpoint protection, stay in force.

Keep existing bounded teardown/recovery contexts and truthful partial outcomes.
There is no guarantee of immediate capacity recovery when Docker is unavailable,
consumers remain, or ownership is uncertain. An interrupted release remains an
obligation selected by existing active/stopping/cleanup-failure recovery; explicit
stop retries a selected old stopped instance. Recovery never restarts workloads,
and no timer or automatic retry loop is added. Network-specific cleanup details
survive unrelated native uncertainty and clear only after verified retry; native
force cannot waive an independent network failure. Destroy/reset/disposable cleanup
must tolerate networks already released while retaining their existing data and
shared-allocation semantics, including disposable data retention during shutdown.

Use the current persistence shape, API and state version: this changes resource
lifecycle, not stored authority or layout. Existing retained network records remain
valid and become eligible through the same selected stop/recovery paths. No global
prune, state-root discovery, cleanup command, address-pool setting, dependency,
public field or background worker is introduced. Activity and bulk-stop work in
[issue 98](https://github.com/octacian/backlot/issues/98) remains separate and can
consume existing cleanup outcomes.

## Consequences

Stopped instances no longer consume avoidable bridge address pools while keeping
application state. Start now needs network creation capacity and may receive a new
Engine ID/subnet. Stable recorded names preserve configuration written through
`network.name` by completed fresh-only initializers, which remain skipped.
Active workloads can still exhaust capacity, and foreign/uncertain resources can
require operator intervention through existing recorded cleanup procedures.

Required checks cover distinct persistent instances and repeated stop/start with
implicit/declared networks, volume/directory witnesses, credential continuity and
fresh-only records, including a persisted `network.name` initializer witness and
recreated names/attachments; foreign same-name collision refusal; old stopped
records; healthy kept work and explicit kept stop;
shutdown/crash recovery; allocation/removal acknowledgment gaps; provider failure,
ownership mismatch and foreign consumers; shared networks and active peers; and
idempotent destroy/reset/disposable success, failure and cancellation. Reuse owned
provider fixtures, register cleanup before effects, track exact IDs and preserve
uncertain residuals. Current lifecycle/help/skill guidance must ship with the fix.

## Alternatives considered

Retain networks until destroy: simplest existing behavior, but ties scarce address
pools to idle data and requires destructive cleanup for routine capacity recovery.
Offer explicit network cleanup only: smaller automatic behavior change, but adds an
operator step/API and leaves ordinary stop accumulating avoidable networks.
Share one instance bridge: reduces allocation pressure but changes isolation and
cross-instance ownership/consumer coordination. Increase address pools: may delay
exhaustion but changes machine configuration and does not fix idle retention.
A global collector/prune would require broader authority and ongoing reconciliation
that this selected-instance lifecycle fix does not need.

## Revisit when

A supported application requires stable Engine network IDs/subnets, active workloads
alone exhaust pools, or exact recorded cleanup cannot provide useful bounded
recovery. Reassess those needs separately from application data retention.

## References

- [Containers and retained state](../containers.md#generations-jobs-and-drift)
- [Resource journal and allocation](../../internal/daemon/resources.go)
- [Execution teardown](../../internal/daemon/runtime.go) and [recovery](../../internal/daemon/runtime_state.go)
- [Docker ownership and consumer checks](../../internal/daemon/docker.go)
- [Provider integration checks](../testing.md#dockerprivate-state-integration)
