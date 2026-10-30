---
title: 'Pin execution-scoped build images from native jobs'
date: '2026-10-25'
status: superseded
summary: 'Hand attempt-specific local image tags to selected native jobs and retain inspected Engine image IDs so concurrent executions do not share mutable image configuration.'
tags: [containers, manifest, planning, lifecycle, persistence, compatibility]
supersedes: []
superseded_by: [20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md]
---

# Pin execution-scoped build images from native jobs

Declare named build images produced by selected native each-start jobs. Supply a
unique attempt tag through an explicit environment reference; after successful
producer completion and native join, inspect that tag at the configured local
Docker Engine and retain its immutable image ID before creating consumers.
Project commands own building, caching and tagging; Backlot owns the handoff and
retained identity without rewriting registered inputs or application configuration.

## Context

Linked worktrees share registered private inputs but need images from their own
source. Disposable invocations also need independent results. Literal/input image
configuration cannot express this handoff without external launch coordination.
Mutable tags alone cannot protect retained consumers from later tag changes.

A mounted consumer's new image ID is unknowable until its producer runs, after
restart has stopped old work. An unchanged reference name or successful build
cannot establish compatibility with retained data. The existing dependency
executor, native supervision and resource generations can support the handoff
without a second scheduler or result service.

## Decision

### Declaration and handoff

Declare each image in `build_images` with one native `each-start` producer. Its
explicit environment receives a `build_image` reference with `field: tag`;
container images use `field: id`. Scenes explicitly select producers; consumers
directly depend on their successful completion. No graph edges are inferred. Validate every
declaration, including unselected ones, under the strict
[manifest contract](../manifest.md#execution-scoped-build-images).
See the [runnable example](../../examples/build-images/README.md) for syntax.

Offline planning preserves symbolic references without tags, image inspection or
producer execution. Valid unselected declarations cause no runtime/provider work.
Existing literal/input image behavior is unchanged.

After admission, generate a fresh tag namespace for each instance, attempt and
image, including concurrent runs in one checkout or different daemon state roots.
Unique tags do not isolate filesystem writes: projects must still declare output
concurrency groups. Producers use existing supervision, deadlines and cancellation.
Even a cached/no-op build must tag its result under the supplied name.

Inspect the producer's complete selected image set only after known successful
exit, native join and confirmation that the attempt is still live. Missing tags,
invalid IDs, failed inspection, unknown/nonzero exits, cancellation or stale
attempts fail the handoff; neither another tag nor a prior pin is a fallback.

Validate the complete candidate set before atomically persisting it, fenced to
the current instance, attempt and resource generation. Record image name,
producer, source attempt, generated tag and immutable ID before consumer creation.
Keep Engine calls outside database transactions. Containers use the accepted ID,
never re-resolve its tag, and fail actionably if that ID is absent. Tags locate
images; they do not authenticate against another trusted Engine administrator or
prevent administrative removal.

### Retention and restart

Accepted pins belong to the retained resource generation, separate from ephemeral
attempt candidates. Expose typed nonsensitive provenance in status/history/export,
without ownership capabilities. A later consumer/readiness failure does not undo
a committed handoff. Admission distinguishes reuse from replacement acceptance:

- First creation, explicit restart, reset and each disposable run may accept
  candidates. A producer may return the same ID; restart does not require a new
  image. Repeated starts of active instances are idempotent and run no producers.
- Stop/start checks retained IDs exist before running each-start producers with
  fresh tags and validating their handoffs. Consumers still use the retained IDs;
  a differing discarded candidate neither replaces the pin nor triggers mounted
  image rejection. Missing pins fail with explicit restart/reset guidance.
- Recovery preserves pins, cleans owned consumers under existing rules and
  requires explicit restart. It never resolves leftover tags, completes an
  uncommitted handoff or implicitly rebuilds/pulls a missing image.
- Reset clears pins with the old generation after consumer join. Destroy removes
  runtime pins with resource cleanup while retaining ordinary history. Neither
  operation deletes project-built images or tags.

On explicit restart, compare replacement IDs for all mounted build-image consumers, including
completed fresh-only jobs that would otherwise be skipped. Same-ID restart needs
no flag. A changed ID requires `restart --allow-image-change`, with a warning that
the application owns compatibility with retained data. The comparison covers all
mount kinds; Backlot does not infer application/schema compatibility.

Without the flag, reject before committing that producer's candidate set or
launching dependent consumers, retaining data, credentials and prior pins. Offer
opted-in restart or reset as remedies. The old execution is already stopped and
earlier components may have run: this is not atomic rollout or rollback of project
side effects. Opting in deliberately accepts possible application-level data loss.

Permission is restart-only and invocation-only, never persisted as reusable
authority. Other request routes reject it. Declaration, argument, initialization,
sensitive-value, provider and ownership fences remain in force, as do handoff and
cancellation checks. Persist a nonsensitive warning with accepted image/attempt
provenance, affected components and old/new IDs; expose it in JSON/status/export
and human restart output. Historical warnings do not authorize another attempt.
Older daemons reject the unknown field; clients never retry without the opt-in.

### Persistence and scope

Advance state 10 to 11 for durable pins/provenance, retaining the cumulative v9
migration path and a genuine predecessor-produced v10 fixture under the
[retained migration policy](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).
Preserve old records with absent pins; migration performs no provider inspection,
image inference, invented provenance or historical file effects. Validate current
pin shapes and links before recovery without imposing new requirements on
historical source decoders. Older binaries reject v11 through the version fence.

Use existing plan/result envelopes, shared API types, stores, scheduling, output
locks and shutdown owners. The restart opt-in is the sole new CLI/API control;
no new endpoint, worker, timer, registry, dependency or cleanup service is needed.

## Consequences

Concurrent builds no longer use shared inputs as invocation output, and retained
consumers are protected from mutable tags. Unselected workloads incur no image
inspection or producer cost, although upgraded daemons retain migration and
validation obligations. Durable identity adds version-fixture maintenance and
requires real-provider concurrency/interruption evidence.

Stop/start may build an unused candidate to preserve each-start semantics;
project caching owns that cost. Default rejection of a changed mounted image
leaves the scene stopped. Explicit acceptance permits application incompatibility
and data loss without weakening the other lifecycle fences.

Operators provide build tooling/cached bases, align the producer with the scene's
local Engine and retain accepted images while needed. Images/tags remain
project-owned after failure, stop, reset or destroy; projects/operators own disk
usage and cleanup. Backlot adds no automatic build/pull, GC, skip-build control,
build-cache framework or general command-output protocol, and injects no Docker
credentials or application HOME/configuration changes.

## Alternatives considered

- **Rewrite private inputs.** Shared worktrees would race on configuration used
  as invocation output; literal/input images remain suitable for prebuilt images.
- **Project-generated tags alone.** Isolates builds but needs external launch
  coordination and does not protect retained launches from tag mutation.
- **Result files containing IDs.** Supports builders unable to tag outputs, but
  adds parsing, ownership, stale-file and cleanup rules that Engine inspection
  avoids for this workflow.
- **Forbid mounted consumers or accept changes with only a warning.** The former
  rejects valid same-ID restarts and intentional compatible upgrades; the latter
  weakens the conservative default. Explicit opt-in preserves both use cases.
- **Skip producers when pins exist.** Saves work but changes declared each-start
  semantics; project caching can avoid rebuilding while still supplying the tag.
- **General results/build subsystem or automatic GC.** Adds scheduling, retention
  and deletion authority without a demonstrated additional result consumer.

## Revisit when

An adopter cannot tag local output, needs another result consumer, automatic
application/data compatibility checks, or a producer-scheduling exception on
stop/start. Reassess before adding result files, remote Engines, automatic
retention/GC or build/retry behavior. Revisit persistence placement if generation
pins and attempt evidence cannot share the existing stores.

## References

- [Issue #80](https://github.com/octacian/backlot/issues/80)
- [Image retention and storage compatibility](../containers.md#execution-scoped-image-retention)
- [Restart opt-in](../daemon.md#build-image-restart-opt-in)
- [Explicit scene lifecycle](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Inherited planning and redaction contracts](20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
- [Testing and provider evidence](../testing.md)
