---
title: 'Bind attached persistent execution to one daemon-owned attempt'
date: '2026-10-22'
status: accepted
implementation: implemented
summary: 'Keep optional attached persistent clients alive with an ephemeral daemon-owned attempt lease and existing retained log delivery.'
tags: [lifecycle, ownership, api, cli, logs]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/50"]
implemented_by: ["https://github.com/octacian/backlot/commit/ec23dcb49279d86f79a2d95f74a03473de43a305"]
---

# Bind attached persistent execution to one daemon-owned attempt

## Context and Problem Statement

Editor play tasks need aggregate scene output and cleanup when their client is
interrupted or killed. Signal handlers alone cannot cover killed clients, while
an instance ID or scene name cannot safely authorize delayed cancellation after
operator restart replaces the attempt.

## Considered Options

- An ephemeral attempt-bound attachment lease in the existing execution owner.
- CLI signals plus ordinary stop: killed clients leave work, and stale requests
  can stop replacements.
- Disconnect-owned long HTTP requests: conflate observation and runtime authority.
- Durable attachments/reconnection: add persistence despite existing recovery
  intentionally interrupting runtime.
- Separate start then attach: leaves an unowned acceptance/replacement interval.

## Decision Outcome

Grant attachment only while accepting a new persistent execution, never by adopting
an independently owned runtime. Bind private random authority and expiry to instance,
exact attempt and daemon generation before launch. Ordinary starts do not acquire
that authority. Keep it in daemon memory and out of ordinary snapshots/logs.

Reuse existing lease timing, expiry coordination, mutation serialization and
cleanup. Delayed renewal/cancellation/expiry cannot revive an ended lease or affect
a replacement attempt. Operator lifecycle commands retain their own authority;
replacement never transfers attachment. Daemon recovery interrupts and does not
resume or automatically reattach.

Share existing bounded retained-log and sampled-state delivery. On client failure,
attempt only fenced cleanup where authority is known, otherwise stop renewal and
rely on expiry. Never fall back to unfenced stop. Preserve original exits and
collection/cleanup uncertainty; a terminal status alone does not prove cleanup.
Observation-only follow/log commands never renew or acquire attachment authority.

### Consequences

Killed clients eventually release runtime while preserving persistent data and
credentials, with no durable attachment store or new expiry service. Stalled output
can lose its lease and stop the scene. Log gaps, sampled transitions and native
cleanup limits remain; no lossless replay, continuing health guarantee or automatic
reconnection is promised.

## More Information

[Attached lifecycle](/docs/daemon.md), [evidence semantics](/docs/evidence.md),
[attachment authority](/internal/daemon/attached.go).
