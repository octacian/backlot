---
title: 'Resolve persistent scenes from recorded identity and sample status'
date: '2026-10-21'
status: accepted
implementation: implemented
summary: 'Resolve persistent scene targets in the daemon from existing identity records and observe bounded sampled snapshots without acquiring lifecycle ownership.'
tags: [lifecycle, identity, api, cli, observation]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/47"]
implemented_by: ["https://github.com/octacian/backlot/commit/859d88167c94a43f0f780284dff804fa028dba5e"]
---

# Resolve persistent scenes from recorded identity and sample status

## Context and Problem Statement

Adopters duplicate pagination and identity matching before persistent-scene
operations. Current-manifest discovery cannot clean up after a deleted/broken
manifest, while choosing the newest or ready record conceals ambiguity.
Observers need useful bounded state without lifecycle ownership or event replay.

## Considered Options

- Daemon-owned resolution over recorded identity and locations, with sampled follow.
- Caller list/filter controllers: duplicate selection and mutation-race decisions.
- Current manifests or project-ID indexes alone: miss retained/broken configurations.
- New persistent indexes, event journals or subscriptions: add writers and recovery
  obligations beyond the required observation guarantee.

## Decision Outcome

Resolve named start/status/stop in the daemon. Start validates current inputs and
uses the existing persistent lifecycle coordinator. Status/stop select existing
records by verified checkout identity and canonical project location, independently
of current manifest/configuration. Use the nearest recorded project ancestor with
no outer fallback; explicit project selection chooses the exact directory.
Zero matches is typed absence, multiple eligible matches is an error, and explicit
recorded IDs remain available for inspection/cleanup. Recheck selection under
mutation serialization before stop; never redirect cleanup to a replacement target.

Moves, replacements and unavailable checkout identity cannot adopt retained state.
Existing identity does not detect replacement of only a project subdirectory;
do not claim a stronger persisted guarantee. Status reads redacted recorded state,
not current readiness or provider health. Existing cleanup, drift and data fences
remain authoritative.

Use bounded polling of finite shared snapshots, with no durable observer or lease.
Named follow re-resolves across absence and recreation while pinning checkout and
daemon generation; ID follow remains pinned to that ID. Identity ambiguity,
replacement, malformed responses and daemon/transport failure end observation
nonzero. Expiry/cancellation stop observation only, never execution. Report sampled
changes truthfully without claiming every transition, replay or automatic reconnect.

### Consequences

Integrations gain broken-manifest cleanup and bounded observation using existing
storage. On-demand retained-record scans grow with history; add indexes only for
measured need. Observers can miss transitions and cannot continue across daemon
replacement. Their failure grants no cancellation or other mutation authority.

## More Information

[Scene operations](/docs/daemon.md), [targeting implementation](/internal/daemon/scene.go).
