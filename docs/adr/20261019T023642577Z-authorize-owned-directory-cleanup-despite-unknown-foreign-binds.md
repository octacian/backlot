---
title: 'Authorize owned directory cleanup despite unknown foreign binds'
date: '2026-10-19'
status: proposed
summary: 'Let authenticated directory ownership authorize cleanup despite uncertain foreign bind identities while preserving positively identified consumer blocks.'
tags: [cleanup, ownership, containers, storage]
supersedes: []
superseded_by: []
---

# Authorize owned directory cleanup despite unknown foreign binds

Authenticated Backlot directory ownership authorizes deletion even when inspected
foreign Docker bind metadata cannot establish a host filesystem identity. This is
the default cleanup policy for supported macOS and Linux runtimes. Continue to
block positively identified overlapping consumers and preserve uncertain ownership
of Backlot resources. Unknown foreign identity is not proof of unrelatedness.

## Context

Docker can retain stopped declarations whose source disappeared, dangling aliases,
or paths in another filesystem namespace. Previously any unresolved foreign source
vetoed directory deletion, including unrelated native disposable jobs, while a
resource-free job could finish normally. Directory ownership and foreign consumer
identity answer different questions. The owner capability authorizes deletion;
inspection can establish known consumers but cannot enumerate every possible alias
or consumer in another namespace.

## Decision

During cleanup, inspect running and stopped container declarations. List and inspect
errors remain blocking (an inspect reporting an already-removed container still
means that container is absent). Preserve a directory when any viable identity
positively overlaps it, including same, ancestor, descendant, resolved symlink and
case aliases. Evaluate candidates independently: failure to resolve one cannot hide
an overlap through another candidate or another mount.

Always evaluate the raw host identity when resolvable. On native macOS, also
evaluate the exact clean `/host_mnt` counterpart only when the configured local
Engine reports Docker Desktop/Linux and that counterpart resolves. Failed or
unavailable namespace verification leaves translation unknown; it does not suppress
a positively identified raw consumer. Unresolved, dangling, noncanonical or otherwise
uncertain foreign identities alone do not block ownership-authorized cleanup.
Retain an unknown classification internally rather than claiming those binds are
proven unrelated. No opt-in flag or new API/state contract is required.

This relaxation applies only to inspected foreign consumers. Keep ordinary project
mount validation and private-state fences strict. Stop and join owned consumers
before deletion; authenticate directory privacy/owner markers and reject replaced
or symlink paths as before. Preserve Docker container capability checks, volume and
network consumer checks, non-forced removal, retry journals and native supervision.
No foreign consumer is stopped or removed implicitly.

## Consequences

Stale declarations no longer make unrelated owned directory cleanup fail or turn
successful disposable jobs into infrastructure failures. Known consumers continue
to retain data and return an actionable reset/destroy diagnostic.

An unknown foreign bind may actually consume the owned directory through an
unidentifiable alias or namespace. Cleanup can delete its backing data. This is an
intentional policy tradeoff: ownership authorizes deletion, and consumer inspection
provides positive overlap protection rather than universal proof of non-use.
Operators must not depend on unknown foreign aliases to retain Backlot-owned data.
Inspection remains subject to concurrent external changes; it is not a kernel lock.

## Alternatives considered

Keeping every uncertain source as a veto prevents routine cleanup because of
unrelated stale declarations. Treating unknown identities as proven unrelated
misstates the evidence and risks hiding a known identity. An opt-in escape hatch
would leave the unwanted default behavior. Forced removal, privileged namespace
helpers and a new storage system add authority or scope without being required.

## Revisit when

Supported Engine metadata provides stronger authenticated host identity, or real
consumer data loss demonstrates that the ownership-authorized tradeoff is unsuitable.
A future platform must establish its own filesystem/provider identity evidence.

## References

- [Runtime ownership and recovery](../containers.md#ownership-and-recovery)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Implementation tracking](../implementation-plan.md#foreign-bind-cleanup-policy)
- [Docker consumer classifier](../../internal/daemon/docker.go)
