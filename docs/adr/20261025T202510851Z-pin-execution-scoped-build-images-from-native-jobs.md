---
title: 'Pin execution-scoped build images from native jobs'
date: '2026-10-25'
status: superseded
implementation: implemented
summary: 'Hand attempt-specific local image tags to selected native jobs and retain inspected Engine image IDs so concurrent executions do not share mutable image configuration.'
tags: [containers, manifest, planning, lifecycle, persistence, compatibility]
supersedes: []
superseded_by: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/80"]
implemented_by: ["https://github.com/octacian/backlot/commit/df8267d14536422c78994f127e531ed6795820b8"]
---

# Pin execution-scoped build images from native jobs

## Context and Problem Statement

Worktrees share private inputs but need images built from their own source, and
concurrent runs need independent outputs. Mutable tags cannot protect retained
consumers. Replacement image compatibility with mounted data is unknowable until
the producer runs, after old work may already have stopped.

## Considered Options

- Attempt-specific build tags with immutable Engine-ID handoff and retained pins.
- Rewrite private inputs: races shared configuration with invocation output.
- Project tags alone: need external coordination and remain mutable.
- Result files or a build subsystem: add parsing, ownership and lifecycle scope.
- Warning-only mounted changes: weakens the selected conservative data policy.

## Decision Outcome

Declare images with explicit native each-start producers and explicit successful
dependencies. Supply fresh per-instance/attempt/image tags through existing
references; projects own build/cache/tag commands and output-concurrency declarations.
Offline plans remain symbolic and unselected declarations perform no provider work.

After known producer success and native join, inspect the complete selected image
set at the configured local Engine. Atomically accept validated immutable IDs under
attempt/resource-generation fences before consumers start. Unknown exits, missing
images, cancellation or stale attempts fail without old-pin/tag fallback. Consumers
use IDs rather than resolving tags; trusted Engine administrators can still remove
or replace image availability.

Retain pins with resource generations. Stop/start runs declared each-start producers
but preserves accepted IDs; discarded differing candidates do not replace them.
Repeated active starts do no producer work. Recovery preserves pins and requires
explicit restart rather than completing/rebuilding uncommitted handoffs. Reset/
destroy clear appropriate pins after joins but never delete project images/tags.

On explicit restart, compare all mounted build-image consumers, including skipped
fresh-only jobs. Same-ID replacement needs no opt-in; changed IDs require invocation-
scoped `--allow-image-change`. Otherwise retain data/credentials/prior pins and reject
before dependent consumers. Earlier work may already have run: rejection is not
atomic rollout/rollback. Opt-in accepts application incompatibility and possible data
loss; retained warnings never authorize another attempt or bypass other fences.

State 11 adds durable provenance under the cumulative v9 migration policy, retaining
a genuine v10 fixture. Migration never inspects providers or invents historical pins.
Use existing scheduling, stores and lifecycle owners rather than a second build service.

The [persistent-start decision](/docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md)
replaces only ordinary-start eligibility and input/attempt selection; other
contracts and existing successors remain in force.

The [attempt-history decision](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)
replaces retention of attempt outcomes and evidence across replacement and destroy.
Other privacy, ownership and recovery guarantees remain in force.

### Consequences

Concurrent builds avoid shared configuration races and mutable-tag retained launches.
Stop/start may build an unused candidate to preserve each-start semantics. A refused
changed image leaves execution stopped; operators own application compatibility,
accepted image availability and disk cleanup. No automatic build/pull/GC is added.

## More Information

[Image retention](/docs/containers.md#execution-scoped-image-retention),
[restart consent](/docs/daemon.md#build-image-restart-opt-in),
[runnable example](/examples/build-images/README.md).
