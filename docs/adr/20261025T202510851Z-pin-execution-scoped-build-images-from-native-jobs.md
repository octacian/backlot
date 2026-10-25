---
title: 'Pin execution-scoped build images from native jobs'
date: '2026-10-25'
status: accepted
summary: 'Hand attempt-specific local image tags to selected native jobs and retain inspected Engine image IDs so concurrent executions do not share mutable image configuration.'
tags: [containers, manifest, planning, lifecycle, persistence, compatibility]
supersedes: []
superseded_by: []
---

# Pin execution-scoped build images from native jobs

Declare named build images produced by selected native each-start jobs. Supply a
unique attempt tag through an explicit environment reference; after successful
producer completion, inspect that tag at the scene's configured local Docker
Engine and retain its immutable image ID before creating dependent containers.
Project commands own building, caching and tagging. Backlot owns the handoff and
retained identity, without writing registered inputs or application configuration.

This decision addresses [issue #80](https://github.com/octacian/backlot/issues/80).
The owner approved the mounted-image restart
opt-in and its later failure timing below; independent architecture review must precede substantial dependent code.

## Context

Linked worktrees share registered private inputs but need different images from
their own source. Disposable invocations also need independent results. At
`a84b98df376195441709360be340540a87b3c45d`, manifest validation restricts container
images to literals or input references. An actual baseline `backlot plan` with a
build-image reference returns `invalid_contract` at `components.test.image`.
This confirms the declaration boundary, not runtime concurrency or provider
correctness. Existing checkout outputs govern filesystem serialization; they are
not image results.

The owner selected generated tags supplied by environment, rather than a result
file or stdout protocol. First creation, explicit restart, reset and independent
disposable executions may accept a new ID; restarting with the same ID is valid.
Stop/start and recovery preserve accepted IDs. Failed, cancelled or stale handoffs
cannot authorize consumers or fall back to an earlier result. Images must exist
locally; no implicit build, pull, substitution or deletion authority is added.

Current `safeRestart` compares image/args for every mounted container before
stopping work. Merely comparing a symbolic build-image reference would allow a
changed resolved image through that fence. The new ID is unknowable until the
producer runs, after restart has joined old work. The owner approved later rejection without an explicit restart opt-in, and
intentional adoption with a compatibility warning when opted in. Storage safety
cannot be inferred from an unchanged reference name or a successful build.
The executor already runs dependency-first, awaits successful jobs, joins their
native groups, persists resource generations and serializes lifecycle mutations.
Those paths are sufficient; a second scheduler or result service is unnecessary.

## Decision

### Declarations and offline behavior

Use a top-level `build_images` map, each value naming exactly one `producer`.
Use `ref: {kind: build_image, name: NAME, field: tag}` only in that producer's
explicit environment assignments (including selected environment sets). Use
`field: id` only in a container's `image`. These names and field spellings are
proposed API details, subject to review. For example:

```yaml
version: backlot/v1
project: application
tools:
  sh: /bin/sh
build_images:
  app:
    producer: build
components:
  build:
    kind: job
    runtime: native
    policy: each-start
    command:
      tool: sh
      args: [-c, 'docker build --pull=false -t "$APP_IMAGE" .']
    environment:
      assign:
        APP_IMAGE: {ref: {kind: build_image, name: app, field: tag}}
        DOCKER_HOST: {literal: 'unix:///explicit/local/docker.sock'}
  test:
    kind: job
    runtime: container
    policy: each-start
    image: {ref: {kind: build_image, name: app, field: id}}
    args: [test]
    depends_on: [{component: build, condition: completed}]
scenes:
  test:
    lifetime: disposable
    components: [build, test]
    terminal_job: test
```

The example's Docker endpoint is illustrative: configure the scene's machine
`docker.endpoint` to the same local Engine and explicitly arrange the project
command's Docker tooling/endpoint. Backlot does not inject Docker credentials or
rewrite application HOME/configuration. Projects may use a configured build tool
instead of a shell. Base images must be available beforehand; project commands
retain their own side effects and responsibility for avoiding unintended pulls.

Validate all declarations, including unselected ones. Producers must exist and
be native each-start jobs; fresh-only producers cannot supply every required
attempt handoff. For each selected consumer require its producer to be explicitly
selected and a direct `completed` dependency. Do not infer graph edges. Existing
cycle validation still applies. Require each selected build image's effective
producer environment to expose its tag explicitly; reject references in unrelated
environments, fixtures, probes, arguments or other value contexts. Require the
bounded name/field shape and prohibit extra reference fields. Multiple images may
share one producer; one image may serve multiple consumers.

Offline plans retain selected declarations and both reference shapes symbolically.
They generate no tags or IDs, contact no Engine, run no producer and require no
result image. Redaction and strict decoding remain in force. Valid unselected
build images cause no provider calls or runtime work. A selected producer tag
reference without a selected image consumer is invalid: there is no handoff to
perform. Existing literal/input images retain their behavior.

### Handoff and retention

After execution admission, derive a valid local tag from the unique instance,
execution attempt and build-image name; use only bounded generated identifiers.
Generate a fresh namespace for each actual attempt, including concurrent runs
in one checkout and different daemon state roots. A project with mutable checkout
build outputs must still declare existing output/concurrency groups; unique tags
do not isolate undeclared filesystem writes.

Run producers through existing native supervision, job deadlines and cancellation.
Only after a known successful exit, successful native join and a live execution
context inspect every selected image for that producer at the configured Engine.
Validate a nonempty immutable local Engine ID. Missing tags, failed inspection,
nonzero/unknown exits, cancellation, unjoined descendants or stale attempts fail
with producer/image context. Never inspect a different tag as a fallback. The
project must tag a cached image under the supplied new tag even on a no-op build.

Validate the producer's complete selected candidate set before persisting it in
one existing database transaction fenced to the current instance/attempt and
resource generation. Persist provenance (image name, producer, source attempt,
generated tag and immutable ID) before any corresponding consumer create. No
Engine call occurs inside a database transaction. Container creation uses only
the accepted immutable ID, never re-resolves the tag, and fails actionably if the
ID is absent. Tags are locators, not authentication against another trusted local
Engine administrator. Concurrent administrative removal may still make create
fail; it cannot authorize substitution.

Keep authoritative accepted pins with the retained resource generation, separate
from ephemeral current-attempt candidate state. Record typed nonsensitive image
provenance in the execution result for status/history/export, without ownership
capabilities or unrelated private values. A failed/cancelled handoff never reads
prior pins as that attempt's result; earlier pins remain retained for diagnosis
and future authorized lifecycle operations. A later consumer/readiness failure
does not undo an already committed successful handoff. Recovery preserves pins,
cleans owned consumers under existing rules and requires explicit restart; it
never resolves leftover tags or completes an uncommitted handoff.

First acceptance in a generation, explicit restart, reset and each disposable run
may accept candidates. Repeated starts of active instances are idempotent and do
not run producers. Reset clears pins with the old generation after consumer join;
destroy drops runtime pins with resource cleanup but retains ordinary history.
Neither operation deletes project-built images or tags.

On stop/start, preserve accepted IDs while each-start
producers still run under their declared policy with a fresh attempt tag and a
validated successful handoff. A candidate from this ordinary start does not replace
the pin; containers use the retained ID. A different unused candidate ID never
triggers mounted-image rejection on ordinary stop/start. Check retained IDs exist before producer
execution; a missing pin fails with explicit restart/reset guidance, without
implicit rebuild. This may build an unused candidate; project caching owns that
cost. The alternative is explicitly suppressing producer work on this lifecycle,
which would require a scheduling exception. Never silently accept a replacement.

For mounted consumers, retain pre-stop declaration, argument, initialization,
sensitive-value and provider fences. Additionally, when an existing generation
has a mounted consumer of a build image and explicit restart is accepting a
replacement candidate, compare the candidate ID with its accepted ID after
producer completion, including consumers that would otherwise
be skipped as completed fresh-only jobs. Same ID requires no special permission.
Apply this comparison to all existing mount kinds, matching the conservative
fence rather than inventing image/data compatibility detection. The resolved-ID
comparison and its rejection/opt-in apply only to replacement acceptance; ordinary
stop/start never rejects a discarded candidate merely for differing from the pin.

On explicit `restart`, `--allow-image-change` authorizes accepting a changed
produced ID for these mounted consumers, with a warning that the application
owns compatibility with retained data. Without it, reject the changed ID before
committing that producer's candidate set or launching dependent consumers;
retain data, credentials and prior pins. Offer explicit opted-in restart or reset
as the remedy. At this late failure the previous execution is already stopped,
and earlier selected components may have run. This is not atomic rollout or
rollback of project-command effects. With the flag, the operator deliberately
accepts the application's possible incompatibility or data loss; Backlot neither
checks application schemas nor weakens other safety checks.

The flag applies only to this invocation's mounted build-image ID comparison.
Keep its boolean in the named RunRequest and in-memory admitted execution state,
not persisted ExecutionOptions or reusable authority. Reject it on run, scene
start, reset, shared-service operations and other unsupported request routes;
missing/invalid handoffs, cancellation, ownership uncertainty and all other drift
checks still fail. Literal/input image behavior is unchanged. No new prompt or
implicit retry is added. Persist a named nonsensitive warning with accepted
attempt/image provenance, identifying affected components and old/new IDs; expose
it in JSON/status/export and human restart output. The warning records an
acknowledged compatibility risk, never permission for another attempt. Repeated
inspection of the accepted attempt can show the warning without reauthorizing
anything. Older daemons must reject the unknown request field; the client never
retries without the opt-in.

### Persistence and engineering boundary

Adding durable pins/provenance is a real persisted semantic change: advance state
10 to 11 and retain the cumulative v9 path. Before implementation, capture a
nonempty closed v10 database using the unmodified predecessor and record its
revision/build/inputs/hashes. Add the smallest 10-to-11 step using the existing
transaction dispatcher: validate the v10 source, preserve existing records, and
represent old generations as having no build-image pins. No provider inspection,
image inference, invented provenance or historical file effects belong in migration.
Absent new fields remain valid for old workloads. Current preflight validates new
pin shapes and links before recovery; retain the historical decoder boundary so
new checks are not applied as v10 source requirements. Older binaries reject v11
through the version fence instead of silently forgetting accepted IDs.

Affected writers are manifest/schema generation, plan/snapshot preparation,
execution admission/result persistence, successful producer handoff, generation
reset/removal and export projection. Admission must explicitly distinguish pin
reuse from acceptance; do not infer permission from the mere presence of a new
attempt. Existing resource/result readers, authority preflight and recovery must
preserve the added records. Use named `api/v1` declaration/provenance types in
existing plan/result envelopes, without a new endpoint or command. The sole new
CLI/API control is the approved restart opt-in above; update parsing, typed request validation and warning output
through the existing CLI/client/daemon boundary.
Runtime scheduling, native supervision, output locks and shutdown owners remain
the existing ones. No worker, timer, watcher, subscription, registry, dependency,
new storage backend or image cleanup process is introduced.

## Consequences

Projects can build concurrently without using shared inputs as invocation output.
Retained consumers are protected from later tag mutation. Existing workloads pay
no image inspection or job cost for this feature; all upgraded daemons still pay
bounded startup migration/validation and retain compatible optional fields.

The implementation crosses manifest/schema, planning, lifecycle, private state,
wire results and evidence projections. It needs actual-provider concurrency and
interruption evidence as well as unit tests. New version fixtures and migration
maintenance are an unavoidable consequence of retained identity. A rejected
post-build storage change leaves a formerly running scene stopped. The owner
approved this timing and the explicit compatibility-risk opt-in; the latter can
allow application-level data loss and must remain narrowly invocation scoped.

Operators provide local Docker/build tooling and cached base images, align the
producer's Engine with machine configuration, and retain accepted images while
scenes need them. Project builds may leave tags/images after failure, stop or
destroy. Operators/projects own their cleanup and disk usage. Backlot neither
claims image ownership from an ID nor provides automatic GC, skip-build controls,
a build-cache framework or a general command-output protocol.

## Alternatives considered

- **Rewrite private input values before launch.** Existing worktrees share the
  registered document; conflates configuration with invocation results and leaves
  adopters coordinating launch races. Keeping literal/input images remains useful
  for prebuilt images but does not solve this workflow.
- **Project-generated unique tags alone.** Can isolate concurrent builds, but
  requires external launch coordination and permits retained launches to follow
  later tag mutation unless the daemon persists immutable IDs.
- **Validated result files containing image IDs.** Supports commands unable to tag
  outputs, but adds output paths, parsing, bounds, ownership, stale-file and cleanup
  rules. The owner's selected tag/inspect protocol provides the needed result with
  an existing Engine operation and no new file lifecycle.
- **Forbid all mounted build-image consumers.** Avoids the resolved storage fence,
  but unnecessarily rejects same-ID restarts and ordinary first-create/reset
  workflows and intentional compatible upgrades. Retain the conservative default
  and the approved explicit restart opt-in.
- **Warn and accept changed mounted IDs by default.** The owner permitted a warning
  or opt-in; chose the explicit `restart --allow-image-change` option to preserve
  the conservative default. Inferring safe upgrades or adding an application
  migration protocol is unjustified; intentional compatibility remains the
  operator/application responsibility.
- **Skip producers when an accepted image exists.** Saves work but conflicts with
  each-start policy and the owner's explicit restart semantics. Project caching
  owns that work.
- **General results/build subsystem or automatic GC.** No demonstrated additional
  consumer; adds schemas, scheduling, retention and deletion authority without
  advancing this bounded image handoff.

## Revisit when

An actual adopter cannot tag its output locally, needs a different result consumer,
needs automatic application/data compatibility checks beyond the explicit opt-in,
or needs a producer-scheduling exception on ordinary stop/start. Reassess scope before adding
result files, compatibility declarations, remote engines, image retention/GC or
automatic build/retry behavior. Revisit persistence placement if actual execution
shows generation retention and attempt evidence cannot share the existing stores.

## References

- [Issue #80](https://github.com/octacian/backlot/issues/80)
- [Manifest and environment contracts](../manifest.md)
- [Container lifecycle and storage compatibility](../containers.md)
- [Native execution and local API](../daemon.md)
- [Architecture and shared API](../architecture.md)
- [Explicit scene lifecycle decision](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Retained cumulative migrations](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Inherited planning and redaction contracts](20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
- [Manifest types](../../api/v1/manifest.go) and [schema constraints](../../api/v1/manifest_schema.go)
- [Validation](../../internal/manifest/validate.go) and [offline resolution](../../internal/plan/plan.go)
- [Executor](../../internal/daemon/runtime.go) and [snapshot persistence](../../internal/daemon/runtime_state.go)
- [Retained fences and generations](../../internal/daemon/resources.go) and [container creation](../../internal/daemon/docker.go)
- [Existing image inspection/pinning analogue](../../internal/daemon/shared.go)
- [State authority preflight](../../internal/daemon/state_authority.go) and [migration dispatcher](../../internal/daemon/state_upgrade.go)
- [Testing and provider evidence](../testing.md) and [external evidence retention](../release-acceptance/README.md)
