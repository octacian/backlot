---
title: 'Resolve persistent scenes from recorded identity and sample status'
date: '2026-10-21'
status: accepted
summary: 'Resolve persistent scene targets in the daemon from existing identity records and observe bounded sampled snapshots without acquiring lifecycle ownership.'
tags: [lifecycle, identity, api, cli, observation]
supersedes: []
superseded_by: []
---

# Resolve persistent scenes from recorded identity and sample status

Add daemon-owned persistent-scene start, status and stop using existing lifecycle
coordination and recorded checkout/project locations. Status and stop do not load
the current manifest or configuration. Observe status through bounded polling of
shared typed snapshots; an observer never acquires stop authority.

This decision extends the accepted lifetime and shared API decisions without
replacing their ownership, readiness, drift or retained-data contracts. The owner
selected the nearest recorded project and named-scene observation across
absence and recreation. Accepted after independent architecture review and
coordinator approval on 2026-10-21; implementation and runtime verification remain
separate delivery obligations.

## Context

[Issue 47](https://github.com/octacian/backlot/issues/47) identifies adopter code
that paginates instances and matches project, scene and checkout before acting.
The daemon already owns persistent deduplication, mutation locks, runtime results
and cleanup. Moving target selection into the daemon removes that duplicated
controller without introducing another execution mechanism.

The existing instance contains `CheckoutIdentity` and a redacted `PlanResponse`
with project ID, manifest path, scene and lifetime. Its recorded canonical project
directory is the parent of that manifest path. bbolt's persistent index keys
project ID + checkout ID + scene; it does not index project location. A project-ID
edit can therefore leave several non-destroyed instances at one location. Destroy
retains historical records but removes the corresponding persistent index entry.
Neither “most recent” nor “currently ready” establishes an unambiguous target.

`plan.Locate` requires a manifest and cannot serve deleted-manifest cleanup.
Checkout identity already combines directory filesystem identity and Git-directory
identity where present; canonical paths detect moves. Project-subdirectory inode
identity is rechecked during start but is not durable. This decision deliberately
uses that existing boundary, rather than adding a new persistent identity schema.

[Issue 50](https://github.com/octacian/backlot/issues/50) concerns an attached
lifecycle owner, including cleanup on client loss. It is separate. The owner has
accepted sampled observation which may miss transitions; durable event history,
replay and streaming transport belong to [issue 54](https://github.com/octacian/backlot/issues/54).

## Decision

### Target selection and lifecycle

Introduce `backlot scene start NAME`, `backlot scene status NAME` and
`backlot scene stop NAME`, with existing `--state-dir`, `--project` and `--json`
conventions. Scene commands take names only; use `backlot status ID` and
`backlot stop ID` for recorded IDs. Add `backlot status ID --follow` for pinned
observation of persistent, disposable and historical instances, including outside
a checkout. Keep finite ID commands and their output envelopes compatible.
No implicit ID/name guessing is needed. This same-PR interface refinement follows
the owner's request to remove scene `--instance` and expose existing-ID follow.

Start resolves and validates current inputs in the daemon, requires persistent
lifetime before preparation/allocation, and delegates to the existing run
coordinator. Repeated/concurrent starts join or return the recorded instance,
retain manifest/config drift reporting and wait for existing readiness semantics.
An unhealthy existing execution requires explicit restart; start does not reconcile
changed storage or retry the application. Scene start cancellation follows existing
run cancellation behavior; status observation cancellation is separate.

For named status/stop, clients send absolute cwd and optional absolute project
selector, never a claimed checkout ID or project ID derived from manifest content.
The daemon canonicalizes filesystem locations, identifies the checkout using the
existing filesystem/Git adapter and compares recorded checkout ID, canonical path
and Git-directory path. Branch and HEAD text remain provenance only. Explicit
`--project` selects the exact directory, or the parent of a recognized manifest
pathname; a deleted manifest pathname is usable when its directory exists.
Canonicalize the directory rather than requiring the missing file to resolve.
Use path-component containment, never string prefixes.

Implicit selection chooses the deepest recorded project-directory ancestor of cwd
in that checkout. Determine project locations before filtering by scene or runtime
state, including destroyed history for project discovery. Do not fall back to an
outer project when the nearest recorded project lacks the scene. Explicit
`--project` chooses the exact recorded directory. Both rules ignore current
manifest content and existence and never choose among project IDs at a location.
For non-Git projects, identify candidate recorded project roots as existing
checkout roots rather than treating an arbitrary cwd subdirectory as a new checkout.

Scan existing instance records in a read transaction, matching the selected
canonical project directory, verified checkout and scene. Include preparing,
prepared, failed, interrupted and stopped persistent records; exclude disposable
and destroyed records from named selection. Zero matches yields typed successful
`not_found`; multiple matches yield a nonzero ambiguity error requiring
`backlot status ID` / `backlot stop ID`. Return no partial “best” match. Do not use only the project-ID-keyed
persistent index, impose the public list page limit or make the CLI paginate.
Corrupt records remain errors; do not skip uncertainty to produce apparent absence.

Moved/replaced checkout roots, unavailable project directories or Git identity
cannot adopt old records by path; fail with an identity/target diagnostic and use
explicit recorded ID for inspection/cleanup. A manifest deletion or project-ID
change alone cannot block recorded status/stop. Replacing only a project
subdirectory inside an unchanged Git checkout is not detectable from the stored
identity: canonical project location remains its selection key. No stronger
persisted subdirectory-identity guarantee is introduced.

Status is one coherent redacted recorded snapshot, including optional execution
attempt, URL and failure details. It performs no readiness probes, provider calls,
lease renewal or current-input validation. Failed execution is status data and does
not alone make status nonzero. Transport, validation, identity and ambiguity errors
do. Do not claim that an absent URL or metadata-only `prepared` state is ready.

Stop resolves one instance ID, acquires its existing mutation lock, then rechecks
identity and the unique selected match before acting. A changed selection conflicts
instead of redirecting cleanup. That recheck is the selection linearization point;
a new project-ID generation accepted later does not redirect the pinned stop.
Stop uses existing cancellation/join, ownership recovery, route removal and data
retention. It shares serialization with start/restart/reset/destroy. Restart on the
same logical ID may precede stop; stop has no attempt-specific attachment authority.
No database transaction or global service lock spans provider cleanup.

Return successful typed `stopped` after verified stop, or `already_stopped` if the
existing cleanup path succeeds without an active runtime. Preserve failed and
metadata-only status instead of fabricating a stopped execution. A cleanup or
collection error remains nonzero, including uncertain failed instances; never
infer success solely from a terminal status enum. Repeated stop preserves private
data and paired credentials. Destroyed records are still inspectable by explicit
ID; stopping their already-clean runtime reports `already_stopped`.

### Shared wire contract and observation

Add named scene target, start request, snapshot/result and follow-record contracts
in `api/v1`, with finite `/v1/scenes/start`, `/v1/scenes/status` and
`/v1/scenes/stop` POST endpoints and thin client/CLI adapters. Requests distinguish
named and explicit-ID targets and reject mixed or missing selectors. Results carry
API version, typed outcome and an optional existing `InstanceResponse` so identity,
status, published origin, failure and drift fields retain one source of truth.
`not_found` has no fabricated instance. Every named result, including `not_found`
before any instance has been recorded, carries the daemon-resolved checkout
identity independently of that optional instance. Resolve the checkout using
existing filesystem/Git rules even when no recorded match exists; if initial
checkout identity cannot be resolved, return an identity error rather than a
successful absence. Validate target/result coherence and redaction on both sides;
preserve existing version negotiation and size bounds. Explicit-ID status reads
one existing inspect record, without named-location discovery or validation, for
either lifetime. Explicit-ID scene stop retains its persistent-only validation;
this reusable API support does not require a duplicate CLI selector.

Each new scene result also carries a random daemon generation created in memory
once at daemon startup. A follower pins that generation and terminates nonzero on
change, including a restart between samples with no observed dial failure. This
adds one ephemeral service field and wire field, not persistent state or an owner
capability. No PID-based daemon continuity claim is needed.

`scene status NAME --follow` and `status ID --follow` share one bounded observer
over the finite scene-status API. JSON mode emits typed
NDJSON with an initial `snapshot`, subsequent changed `snapshot` records and one
`complete` or `error` terminal record when stdout remains writable. Snapshot data
uses the same scene result contract. Records include sampling time; compare the
snapshot payload excluding sampling time so idle polling emits no duplicates.
Changes include instance/attempt identity, status, components, origin and failure
details, not just the top-level status enum. No record sequence claims to count
runtime transitions. Human output observes the same snapshots and outcomes.

Named follow re-resolves the original canonical cwd/explicit-project selector and
scene on every sample. Continue through successful `not_found` snapshots and
stopped/destroyed/recreated gaps until bounded expiry or cancellation. A newly
recorded unique match can therefore have a new instance ID, which every snapshot
must expose. The owner explicitly selected this behavior rather than pinning the
first instance. Pin the daemon-resolved checkout ID, canonical checkout path and
Git-directory path from the initial named result, including an initial
`not_found`, and compare that baseline against every subsequent sample. Exclude
HEAD/branch provenance from continuity comparisons. The daemon independently
resolves and verifies current identity for every request; the observer's baseline
comparison grants no target-selection or mutation authority and is never a
caller-supplied ownership claim. A replaced/moved checkout, ambiguity or
unavailable identity is a terminal error, never permission to adopt another
checkout. This baseline is observer memory only, with no persistent registration
or subscription. Explicit-ID follow never re-resolves a name or checkout: it
keeps the requested ID across destroy/recreation and can report destroyed history.
It uses the same daemon-generation fence, bounds and terminal records. Stopped/failed states and initial absence do not finish observation.
Neither mode acquires mutation authority or claims every transition was seen.

Use interval default 1s, inclusive bounds 100ms through 1m; duration default 30m,
inclusive bounds 1s through 24h. Reject zero, negative, malformed and out-of-range
values. Start the total budget before initial acquisition; one synchronous
cancellable request is in flight at a time and the next sample waits for the
interval after completion, without catch-up bursts. Per-request timeout remains
bounded by the existing client timeout and remaining total duration. Expiry emits
typed `complete` with reason `expired` and exits successfully. It is not proof of
a healthy scene. A timer stops observation; it never stops execution.

SIGINT/SIGTERM cancel in-flight observation, release its timer/client, emit typed
`complete` reason `cancelled` when possible and exit 130/143. Parent cancellation
is cancellation, with a nonzero cancelled exit. A daemon/transport failure emits
typed `error` and exits nonzero; never relabel it expiry or cancellation just because
a signal/deadline arrives later. Establish a deterministic classification for a
request actually ended by the local observation budget. Reject malformed responses,
API/generation changes and target ambiguity. No retries, reconnect loop or daemon
autostart; a new CLI invocation is required after failure. Output errors terminate
nonzero and cannot guarantee a final record on a closed pipe.

Follow owns only a CLI/client observation loop. It never calls runtime stop,
renew, cancel or destroy, never persists a subscription and never creates a daemon
observer worker. Closing stdout, killing the observer or expiry leaves execution
under its existing lifetime owner.

## Consequences

Existing storage is sufficient: no schema/version change, migration, index,
background reconciliation, event journal or runtime dependency is proposed. Target
reads scan retained records on demand; cost grows with historical instance count.
Existing lifecycle writers and recovery remain the sole persistent writers.
The ephemeral daemon-generation assignment is the only new daemon-lifetime write.
No new work runs when the feature is unused; existing lifecycle/lease/retention
workers keep their contracts. Each active follower adds bounded polling requests,
bbolt reads and, for named targets, filesystem identity reads and retained-record
scans, with no provider probes.

Operators gain cleanup despite broken/deleted manifests, but unavailable identity
still requires recorded IDs. They gain sampled state, not every transition or
continuation across daemon replacement. No new service, credentials, machine
configuration, resource grant or monitoring job is needed. Existing state/privacy,
provider setup, data retention, ownership and manual recovery remain unchanged.
Decision acceptance does not establish implementation completeness or runtime
verification; those remain in the linked issue and implementation tracking.

## Alternatives considered

Keep adopter-side list/filter code: it works today but repeats pagination,
identity, ambiguity and mutation-race decisions outside the daemon's authority.
A daemon resolver over existing records is the narrower durable fix.

Reuse current manifest discovery for every operation: little new query code but
cannot stop a scene after manifest deletion or select an older project-ID record.
Requiring explicit IDs for all cleanup retains the integration burden in issue 47.

Add a project-location index or durable project identity now: potentially cheaper
queries or stronger subdirectory-replacement detection, but expands every relevant
writer, state compatibility and recovery without being needed for existing checkout
identity plus recorded location. Reconsider only with concrete evidence.

Use the current project-ID persistent index alone, or choose newest/ready record:
small lookup cost but either misses old identity or conceals ambiguity. Excluding
only destroyed history keeps recreation usable without treating failure as absence.

Use durable event streams, resumable subscriptions or attachment leases: stronger
history/continuity or client-loss cleanup adds writers, retention and lifecycle
coordination that sampled observation does not need. Issues 54 and 50 own those
separate requirements. Polling with a finite budget is sufficient here.

Requiring explicit project paths whenever recorded projects are nested avoids
implicit selection but burdens ordinary worktree usage; the owner chose nearest
recorded ancestry with no outer fallback. Pinning named follow to its first
instance is simpler to explain as one execution history, but misses the chosen
named-scene indicator across recreation; explicit IDs still provide pinned views.

## Revisit when

Recorded-instance scans demonstrably harm interactive status latency; stored
project-location identity proves insufficient for an actual adopter; consumers
need transition history or resumable daemon observation; or attached ownership
requires a separate attempt-bound authority. Reopen the relevant scope before
adding indexes, persistent identity, worker lifetimes or subscriptions.

## References

- [Targeting issue 47](https://github.com/octacian/backlot/issues/47)
- [Attached owner issue 50](https://github.com/octacian/backlot/issues/50)
- [Streaming follow-up 54](https://github.com/octacian/backlot/issues/54)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Shared typed API](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Native ownership](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Project discovery](../../internal/plan/files.go)
- [Checkout identity](../../internal/daemon/identity.go)
- [Existing instance storage](../../internal/daemon/state.go)
- [Lifecycle coordinator and mutation locks](../../internal/daemon/runtime.go)
- [Destroy and index removal](../../internal/daemon/resources.go)
- [Shared instance contracts](../../api/v1/daemon.go)
- [Current client transport](../../internal/client/client.go)
- [Canonical daemon command guide](../daemon.md)
