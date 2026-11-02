---
title: 'Authorize owned directory cleanup despite unknown foreign binds'
date: '2026-10-19'
status: accepted
implementation: implemented
summary: 'Let authenticated directory ownership authorize cleanup despite uncertain foreign bind identities while preserving positively identified consumer blocks.'
tags: [cleanup, ownership, containers, storage]
supersedes: []
superseded_by: []
related: []
tracking: []
implemented_by: ["/internal/daemon/docker.go"]
---

# Authorize owned directory cleanup despite unknown foreign binds

## Context and Problem Statement

Docker may retain bind declarations with missing sources, dangling aliases or
paths in another filesystem namespace. Treating every unresolved foreign source
as a veto blocks unrelated owned cleanup. Directory ownership and foreign consumer
identity answer different questions.

## Considered Options

- Ownership-authorized cleanup with positive known-overlap protection.
- Preserve every directory for unknown foreign identity: indefinitely blocks
  routine disposal because of unrelated stale declarations.
- Treat unknown as unrelated: misstates evidence and can conceal a known overlap.
- Forced provider removal or privileged namespace helpers: add unnecessary authority.

## Decision Outcome

Authenticated Backlot directory ownership authorizes deletion despite uncertain
foreign bind identity. Inspect running and stopped declarations; list/inspect
failures still block. Preserve positively identified same/ancestor/descendant
consumers, including symlink/case aliases. Evaluate identities independently so an
unresolved candidate cannot hide another candidate's known overlap.

Always consider resolvable raw host identity. Interpret Docker Desktop namespace
counterparts only when verified for the configured Engine; failed translation
remains unknown. Unknown alone is neither a veto nor proof of non-use.

This relaxation applies only to foreign-consumer inspection. Preserve strict
private-state and project mount validation, authenticated directory ownership,
joined owned consumers, resource capabilities and retry journals. Never stop or
remove a foreign consumer implicitly.

### Consequences

Stale foreign declarations no longer turn unrelated cleanup into infrastructure
failure. An unidentifiable alias may nevertheless consume the directory and lose
backing data: this is an intentional ownership-policy tradeoff. Inspection is
not a kernel lock against concurrent external changes. Reconsider if actual data
loss or stronger Engine identity evidence changes that tradeoff.

## More Information

[Ownership and recovery](/docs/containers.md#ownership-and-recovery),
[consumer classifier](/internal/daemon/docker.go).
