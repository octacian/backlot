---
title: 'Release stopped instance networks while retaining application data'
date: '2026-10-28'
status: accepted
implementation: implemented
summary: 'Release authenticated instance networks after verified consumer teardown and recreate them on later start to avoid idle address-pool consumption without deleting retained data.'
tags: [containers, networking, lifecycle, cleanup, recovery, compatibility]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/96"]
implemented_by: ["https://github.com/octacian/backlot/commit/685b7af7f2f8d883f9ba25bd9819ff227012b81a"]
---

# Release stopped instance networks while retaining application data

## Context and Problem Statement

Stopped persistent instances retain empty networks and consume finite Docker
address pools. Persistent data retention does not require stable network
allocations; releasing capacity should not force application-data destruction.

## Considered Options

- Release instance networks on verified teardown and recreate on start.
- Retain until destroy: couples capacity recovery to data deletion.
- A separate cleanup API: leaves ordinary stop accumulating reservations.
- Shared bridges or larger address pools: change isolation/machine settings without
  addressing the lifecycle cause.

## Decision Outcome

Treat instance networks as runtime resources. Release only selected recorded
networks during teardown, explicit stop, shutdown and interrupted recovery; older
stopped records are handled on explicit stop, without a historical sweep. Healthy
kept services retain networks; shared-service networks remain separately retained.

Join consumers and authenticate recorded capability/Engine IDs before removal.
Preserve foreign consumers, uncertain ownership and incomplete teardown with retry
obligations; settle only verified removal or authoritative absence. Native force
cannot waive independent network failures. Never disconnect foreign declarations.

Recreate through existing intent/effect ownership before launching consumers,
retaining stable names while recording new IDs. A foreign same-name collision
fails rather than grants adoption authority. Preserve instance/data generations,
storage, credentials, initialization records and image pins; recovery never resumes
workloads. Existing reset/destroy/disposable data semantics remain unchanged.

### Consequences

Stopped instances return avoidable address capacity while retaining application
state. Later starts need capacity and may get new IDs/subnets, although names stay
stable for completed initialization. Active workloads, foreign consumers and
uncertain resources can still exhaust pools or need operator intervention. No new
collector, bulk cleanup command or address-pool policy is required.

## More Information

[Container lifecycle](/docs/containers.md#generations-jobs-and-drift),
[resource journal](/internal/daemon/resources.go).
