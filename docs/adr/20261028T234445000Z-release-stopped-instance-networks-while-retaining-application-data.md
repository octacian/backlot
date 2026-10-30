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
them after verified consumer teardown and recreate them on later start. Retain
application data and recorded network names; Engine IDs and subnets may change.

## Context

[Issue 96](https://github.com/octacian/backlot/issues/96) reports Docker address-pool
exhaustion. Owned fixtures confirmed that stopped persistent instances retain empty
networks, while disposable completion and destroy clean them up. This establishes
avoidable idle retention, not the cause of every allocation failure; execution
evidence is in [PR 104](https://github.com/octacian/backlot/pull/104).

The [lifecycle decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
requires persistent data retention, not stable network allocations.

## Decision

Release only the selected instance's recorded networks during normal teardown,
explicit stop, daemon shutdown and interrupted-execution recovery. Explicit stop
also handles older stopped records; there is no historical sweep. Healthy kept
services retain their networks until explicitly stopped. Project-scoped
[shared-service networks](20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
remain separately retained.

Join owned consumers before release. Reuse existing capability/Engine-ID
authentication, consumer fences, lifecycle locks and the intent/effect journal.
Preserve foreign consumers, including never-started declarations, without
disconnecting them. Provider errors, uncertain ownership and incomplete teardown
retain resources and return bounded cleanup failures with recorded retry authority.
Settle obligations only after verified removal or authoritative absence; native
force cannot waive an independent network failure. Recovery never restarts work.

Recreate released or authoritatively absent networks through the existing journal,
preserving their names and recording effects before launching consumers. A foreign
same-name collision fails startup; names alone grant no adoption or deletion
authority. Retain the instance/data generation, storage, credentials, fresh-only
completion records and build-image pins. Existing storage verification, endpoint
drift protection and reset/destroy/disposable data semantics remain unchanged,
including disposable data retention during shutdown.

Use the existing state layout and API. No collector, cleanup command or machine
address-pool configuration is needed; bulk activity/stop work remains in
[issue 98](https://github.com/octacian/backlot/issues/98).

## Consequences

Stopped instances release avoidable address-pool reservations without deleting
application state. Start needs creation capacity and may receive a new Engine
ID/subnet. Stable names preserve configuration written through `network.name` by
completed initializers. Active workloads and foreign or uncertain resources can
still exhaust capacity or require operator intervention.

## Alternatives considered

- **Retain until destroy:** preserves allocations but couples capacity recovery to
  application-data deletion.
- **Separate network cleanup operation:** leaves ordinary stop accumulating idle
  networks and adds a manual step/API.
- **Shared bridges or larger address pools:** change isolation or machine settings
  without addressing the instance lifecycle.

## Revisit when

Supported applications require stable Engine IDs/subnets, active workloads alone
exhaust pools, or recorded cleanup cannot provide useful bounded recovery.

## References

- [Container lifecycle and ownership](../containers.md#generations-jobs-and-drift)
- [Resource journal](../../internal/daemon/resources.go) and [consumer checks](../../internal/daemon/docker.go)
- [Provider checks](../testing.md#dockerprivate-state-integration)
