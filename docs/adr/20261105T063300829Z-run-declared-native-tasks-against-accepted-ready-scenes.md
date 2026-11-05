---
title: "Run declared native tasks against accepted ready scenes"
date: 2026-11-05
status: accepted
implementation: in-progress
summary: "Bind explicit finite native tasks to one accepted parent attempt and resource generation, with independent execution outcomes and dependent cleanup fences."
tags:
  - lifecycle
  - native
  - state
  - security
supersedes: []
superseded_by: []
related: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261103T174043965Z-reassociate-retained-projects-through-explicit-current-bindings.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md, /docs/adr/20261103T174849392Z-select-bounded-working-directories-for-native-commands.md, /docs/adr/20261104T030614332Z-compose-bounded-declared-values-for-explicit-consumers.md]
tracking: ["https://github.com/octacian/backlot/issues/111"]
implemented_by: []
---

# Run declared native tasks against accepted ready scenes

## Context and Problem Statement

Administrative application commands need a ready scene's allocated endpoints and
paired credentials. External wrappers currently select the scene, retrieve secrets
and construct a native environment. A declared command should consume those facts
inside the daemon without restarting the scene or gaining authority over its data.
Existing scene execution owns allocation and teardown; invoking it again is not
equivalent to borrowing a ready generation.

## Considered Options

- Explicit tasks borrowing an accepted generation through the existing execution
  owner, native supervision and evidence machinery.
- Keep application wrappers and sensitive retrieval: no daemon change, but retains
  the process-to-process plumbing and secret round-trip this feature removes.
- Use a separate disposable scene: useful for independent resources, but allocates
  another generation rather than borrowing the selected scene's paired facts.
- Run the retained command without checking current declarations: smaller admission
  logic, but silently executes stale intent after an operator edits the task.
- Add hooks, task dependency execution or a general command runner: widens lifecycle
  authority beyond one declared finite invocation.

## Decision Outcome

Enable existing native each-start job components explicitly through a persistent
scene's `tasks` selection. A task cannot initialize resources or produce build
images. Selection alone never adds it to `scene.components` or the automatic job
graph; if independently selected there, existing startup policy still applies.
Fresh-only initialization remains exclusively in that graph. Tasks borrow only
resources and services already selected by the parent, and declare their own
outputs and artifacts through existing component contracts.

Expose `backlot task run TASK --scene SCENE` or `--instance ID`, with existing
project targeting and literal argv forwarding after `--`. Require one unambiguous
recorded persistent parent, already ready. Never start, restart, reconfigure or
substitute another parent. Return an independent task execution identity and
typed result; inspection, lease renewal and cancellation address that execution.
Missing mutation replies mean unknown acceptance, with no automatic retry.

Capture all enabled task plans and their resolved private inputs alongside the
scene's accepted snapshot, keeping allocation references symbolic. Enabled tasks
undergo the existing tool, cwd, seed, required-input and reference validation even
if never invoked: enabling one commits to its usable captured configuration.
Unenabled reusable jobs receive existing structural validation, without resolving
their task-only tools, directories or inputs. Task-only outputs are validated but
are not reserved by parent startup. No task allocation or command runs at capture.

At invocation, compare the selected task's current effective declaration and
configuration, including input/seed values, executable selection, canonical cwd,
output paths and referenced parent contracts, with its accepted capture. Changes
to unrelated tasks or scenes alone do not invalidate it. Missing or incompatible
current inputs refuse execution with guidance to explicitly restart and accept
the declaration; protected changes still require existing reset/destroy rules.
Execute accepted values only, never combine a new task definition or newly read
credentials with old allocations. Snapshotless legacy task capture means no enabled
tasks until an explicit new parent attempt; retained manifest text alone is not
proof of capture. Ordinary joins do not retrofit tasks into a running attempt.

Bind acceptance durably to the exact parent instance, attempt and resource
generation, including the resource-free case. Resolve values as a native consumer
using that parent's ports, publication and accepted paired allocation credentials;
do not copy container environments or provision missing allocations. The task's
execution identity remains distinct from the parent `instance.id` reference.
Preserve composition sensitivity, sensitive argv rejection, direct argument
boundaries, accepted cwd checks and secret-safe public discovery, errors and
results. Application logs/artifacts retain their existing sensitivity boundary.

Dependencies are predicates on the accepted parent graph, never instructions to
launch work. Service dependencies require `ready`; job dependencies require the
parent's established successful completion or valid retained fresh-only
initialization satisfaction. Reject dependencies on other task invocations or
unselected components. Admission checks the parent's ready state and the required
live services, without a new probe scheduler or continuous-health guarantee.
Parent termination cancels dependents; later application failures retain their
own task outcomes.

Serialize admission with parent lifecycle mutation and atomically register the
dependent before launch. Recheck parent identity, readiness and generation at
acceptance; a replacement causes conflict, never retargeting. Hold serialization
only for admission, not the task lifetime, so disjoint tasks can run concurrently.
Reserve task outputs immediately or reject: any group held by the parent or another
execution, or overlapping canonical paths, conflicts even under renamed groups,
symlink aliases or declaration drift. Use accepted path reservations and current
canonical checks so retargeting cannot erase a held conflict. Reuse checkout scope
and path containment; no blanket bypass or lock queue. Undeclared application writes
and hostile concurrent filesystem mutation acquire no new sandbox guarantee.

Parent stop, restart, reset, destroy, attached cancellation, failure teardown and
daemon shutdown fence new admissions, cancel and join dependent task owners and
collectors before releasing services, networks, allocations or data. The same
ordering applies during recovery and shared-binding repair. Failed or uncertain
task cleanup retains parent resource and output fences; a collection-only failure
does not imply a live consumer. Task cancellation, expiry and completion clean up
only task-owned work, never the parent's resources or lifecycle. Use the existing
finite client lease, independent expiry coordinators, bounded joins and native
guardian intent/effect protocol; preserve exact task exits separately from
collection and cleanup failures. There is no task keep-on-failure mode.

Persist task journals and parent links in the existing daemon database before
activation, with attempt-fenced result writers independent of the parent's current
result. Recovery interrupts tasks and reconciles their authenticated effects
before parent release; it never resumes them or reconstructs authority from public
history. Extend the cumulative state migration and version fence so older binaries
cannot ignore dependent consumers. Preserve genuine predecessor fixtures and do
not fabricate task captures or past outcomes during upgrade.

Use existing history, evidence budgets and deletion-intent retries. Completed tasks
count within their parent's completed-history retention scope, rather than creating
unbounded one-record instance histories. Active and cleanup-uncertain task records
remain protected. Parent restart/reset retains diagnostic history under existing
limits; successful parent destroy erases dependent task outcomes, logs, artifacts
and private launch material as well as parent history. Failed erasure remains a
durable failure. Settled task private launch inputs are not a historical secret
archive; keep only material still required for cleanup and existing public history.

### Consequences

This adds accepted task capture, independent execution records and parent-dependent
admission/recovery obligations to existing writers. Native launch, value resolution,
leases, evidence and retention are reused, but scene allocation/teardown cannot
be reused wholesale for borrowed resources. CLI, shared API, schema, discovery,
reference guides and installed skills must change together. No new dependency,
store service, scheduler or lifecycle engine is introduced.

Enabled but unused tasks can prevent acceptance when their required configuration
is unavailable. Parent lifecycle commands can fail while dependent cleanup remains
uncertain, and protected records can exceed retention caps. Unused scenes without
tasks launch no additional work; the migration and shared lifecycle checks still
exist. Resource borrowing does not authorize or roll back application-level
administrative mutations, nor strengthen cooperative native containment.
