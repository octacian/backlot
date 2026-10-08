# Backlot V1 product requirements

Status: agreed product scope, ready for phased implementation. This document
describes required behavior; the repository scaffold is not a completed V1.

## Purpose

Backlot coordinates isolated development and testing environments for humans and
coding agents. Projects declare what they need; a local daemon handles execution,
resource allocation, readiness, routing, logs, evidence, and cleanup.

Git worktrees isolate source but do not inherently isolate ports, browser
origins, databases, mutable storage, build output, or process lifetimes. Backlot
centralizes those lifecycle guarantees so each project can use a small declarative
manifest plus its existing application commands and preparation code.

Backlot is a general-purpose public project at `github.com/octacian/backlot`,
licensed under BSD 3-Clause. No particular application's migration is a release
gate. Acceptance uses self-contained, generic fixtures maintained here.

## V1 boundary

Ship a Go daemon and CLI in one executable for macOS ARM64. A future UI must be
able to use the same typed API. Keep OS-specific process and transport operations
behind explicit boundaries so Linux and native Windows can follow later.

Operate on one machine with a local Docker engine. Publishing uses an existing
local Caddy server, running natively or in a local container, with an owned domain
and trusted certificates configured by the operator. LAN access to applications
is compatible with this model; daemon administration stays local to the user.

Required workflows:

| Workflow | Behavior | Lifetime and data |
| --- | --- | --- |
| Interactive development | Native services and their existing watchers, optionally mixed with containers | Persistent, isolated per checkout and scene |
| Production preview | Production application topology, updated by explicit restart | Persistent, independent from development |
| Backend integration | Run a selected database-backed test command | Disposable, fresh resources per invocation |
| UI integration | Prepare a production-like stack and run selected browser tests | Disposable, fresh resources with collected evidence |

Scene names are project-defined. These workflows do not impose language,
framework, database engine, or test-runner conventions on adopting projects.

## Concepts

- **Project:** a versioned JSON or YAML manifest defining reusable components and
  named scenes. Each project declares a stable identifier.
- **Scene:** a workflow selecting services, jobs, resources, environment mappings,
  readiness requirements, and an explicit `persistent` or `disposable` lifetime.
- **Instance:** one actual scene execution with an identity, owned resources,
  attempts, logs, status, and optional canonical origin.
- **Service:** a long-running native process or generic container.
- **Job:** finite native or container work, such as build, migrate, seed, or test.
- **Resource:** an instance-owned volume, directory, generated secret, network,
  port allocation, or published origin. A database is a generic container with
  explicit image/environment/storage/probes, not a specialized V1 resource.
- **Machine configuration:** executable overrides, local Docker access, Caddy
  endpoint and route scope, upstream reachability, domain suffix, and daemon
  storage settings. Machine credentials never belong in a project manifest.

Reuse component definitions across scenes without arbitrary scripting or opaque
inheritance inside the manifest. Application-specific commands and scripts remain
valid inputs. V1 does not offer manual retriggering of arbitrary jobs.

## Project discovery and identity

Discover the manifest from the working directory upward, bounded by the checkout
root, with an explicit project-path override for automation. Accept one manifest
per project root: `backlot.yaml`, `backlot.yml`, or `backlot.json`; multiple matches
are an actionable error. Both formats represent the same schema.

A persistent instance is unique to project + checkout + scene. Different Git
worktrees must resolve to different checkout identities even when they use the
same branch or repository. Canonicalize path aliases so a symlink does not create
a second environment for the same checkout. Branch name is display/provenance
metadata, not ownership. Explicitly handle removed or moved checkouts; do not
guess that a path or branch match authorizes adopting or deleting resources.

Disposable invocations receive unique run identities. Concurrent invocations
from the same checkout must not share mutable resources. Record checkout path,
available Git provenance, manifest/config digest, and the resolved execution
plan. Preserve this snapshot so later manifest changes cannot redirect cleanup.

## Manifest and configuration

Use an explicitly versioned, strictly validated schema. Reject unknown fields,
invalid references, dependency cycles, conflicting declarations, and unsatisfied
required inputs before allocating resources. Report field/component context.

Declare executables and argument arrays directly. Native commands may use pnpm,
npm, Go, or any configured executable; no mandatory wrapper script or implicit
shell. Shell interpretation requires an explicit shell command. Version managers,
toolchain installation, and automatic project dependency installation are out of
scope. Do not assume the daemon inherits directory-aware interactive shell setup.

Environment configuration is read-only seed files in `KEY=value` format plus
explicit component assignments and selected inherited values. Seeds are not
sourced as shell scripts. Shared environment sets require explicit inclusion.
Apply the native launch baseline (containers retain image defaults), selected
pass-through values, seed files in declaration
order, and explicit component assignments in that order. Resolve dynamic
references in the assignments from the allocated instance plan. Do not dump the
daemon's full environment into application processes.

Make application URL, service ports, resource paths, image references, and secrets
available through named references. Validate required seeds/values and executable
availability. Keep admin and application credentials separated through explicit
environment mappings. Persist generated values for retained resources; ordinary
restart must not rotate a database's credentials independently of its storage.

Backlot never creates, replaces, or deletes application configuration files in
the checkout in V1. Applications may read their existing static config and accept
dynamic environment overrides. Backlot maintains its own runtime state and
evidence outside checkouts. Application commands may produce their declared build
output; Backlot itself does not rewrite project files.

## Execution and dependencies

Support a directed graph with explicit dependency conditions: service ready or
job completed successfully. Initialization jobs run only for fresh resource
generations; record initialization success only after completion. Jobs intended
to run at each start, such as migrations or builds, are declared separately.
Applications own migration/fixture correctness and repeatability.

Services use declared probes with deadlines, such as TCP, HTTP, or command probes.
An alive process alone is insufficient. Support an aggregate scene probe after
route registration so server-side rendering through the public origin does not
introduce a startup cycle. Readiness means observed availability, not proof that
a native watcher compiled the latest source successfully.

Launch native work with verified ownership and cancellation within a dedicated
supervised process group. Native commands and all descendants (including
readiness commands) must stay in that group: detachment through setsid/group
changes and delegation to unrelated supervisors are unsupported. This is a
cooperative command contract, not kernel containment; detecting or cleaning up
deliberately escaped descendants is not guaranteed. Successful stop verifies the
complete supervised group is empty; uncertain ownership remains untouched and
returns failure. See the [native supervision decision](adr/20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md). Create
generic containers, networks, and volumes directly through Docker, using existing
images or project Dockerfiles/build commands. Delegate image building to existing
Docker tooling; do not implement a build engine. Record actual engine IDs and
ownership markers before exposing a usable instance. Disable automatic container
restart policies for Backlot-owned services.

Allocate ports with bind-conflict handling and bounded retry. Native applications
must accept their assigned ports. Never kill an unrelated port owner. A free-port
check alone is not a reservation; if consumers cannot inherit a listener, detect
launch conflicts and retry safely.

Declared checkout-output concurrency groups serialize operations sharing mutable
build locations. Backlot cannot infer arbitrary command side effects. Fully
isolated databases do not make shared build directories concurrency-safe.

## Networking and HTTPS

Only scenes declaring a published HTTP origin require Caddy. Docker is required
only for selected container work. The daemon remains usable for diagnosis when
providers are unavailable; affected scene starts fail with actionable messages.

Allocate a distinct hostname per published instance under the configured suffix.
Keep the instance label within the operator's wildcard certificate depth. A
logical persistent instance keeps its hostname across stop/start and reset.

Declare a bounded routing structure: hostname, path match, target service/port,
and explicit prefix-preservation or stripping. Permit a frontend and backend to
share one canonical HTTPS origin. HTTPS alone does not remove CORS requirements;
same-origin routing avoids cross-origin configuration when the application fits.
Raw Caddyfile import and arbitrary Caddy configuration are outside V1.

Register routes only inside the configured Backlot-owned scope. Preserve unrelated
gateway configuration and handle concurrent edits. Never stop or reload the whole
gateway to stop a scene. Treat DNS, certificate issuance/renewal, and system trust
as operator responsibilities. Validate published origin reachability from both
host and container consumers when applicable, including TLS validation and SSR.

## Lifecycle and concurrency

`run` for a persistent scene starts it, waits for readiness, and returns identity,
URL where applicable, and status. Closing the CLI leaves it running. Concurrent
or repeated starts of the same scene join the existing start or return the ready
instance. An unhealthy instance reports failure and requires explicit restart.

`run` for a disposable scene creates a new instance, waits for its designated
terminal job, gathers evidence, and tears down owned resources. Forward arguments
after `--` to that terminal job only. Keep the original test exit status in the
result. Ctrl-C requests cancellation, stops the full owned process trees, collects
available evidence, and cleans up. A disconnected client must not leave an
unbounded detached test: define and test a daemon-enforced client lease with a
bounded disconnect grace period. Persistent startup is daemon-owned once accepted.

Lifecycle commands:

| Command | Contract |
| --- | --- |
| `stop` | Stop runtime and remove routes; preserve persistent data |
| `restart` | Stop/start with retained data; rerun each-start jobs, not fresh-only initialization |
| `reset` | Stop, replace mutable resource generation, initialize, and start; retain logical identity/hostname |
| `destroy` | Stop and remove owned runtime/data; retain evidence until its retention policy or explicit removal |

Reject ambiguous targets. Outside a checkout, target instances by their recorded
IDs. Serialize conflicting lifecycle mutations on an instance. Define handling
of manifest drift: repeated `run` reports drift; explicit restart reconciles
compatible changes. Changes to retained storage or initialization contracts that
cannot preserve data safely require an explicit reset/destroy, never an implicit
destructive recreation.

No automatic service restart or job retry. Unexpected exits make required services
and the scene unhealthy. Loss of a required dependency during a disposable run
fails and cancels the run. Existing native watchers may restart their own child
application, but Backlot does not add another retry loop.

Graceful daemon shutdown stops environments, cancels runs, and preserves retained
data. After a crash, reconcile recorded resources and stop surviving work only
when ownership can be verified. Mark affected instances interrupted and require
explicit restart. Leave uncertain resources untouched with diagnostics. Persist
allocation intent and completion so crashes between external mutations and local
recording do not silently erase ownership or create false success.

Cleanup is ownership-scoped and idempotent. Names or PIDs alone are insufficient.
Do not delete resources while an owned consumer may still be running. Report
cleanup failures and recovery targets; never claim successful teardown on error.

## Logs, evidence, and results

Provide `list`, `status`, `logs`, and `doctor` alongside lifecycle commands.
`logs <instance> --follow` streams live output; `logs <instance>` reads captured
output after completion or teardown. Filter by component and identify instance,
component, timestamp, stream, and attempt. Retain native and container logs outside
their runtime resources; do not depend solely on logs from a container that will
be removed. Surface collection gaps and daemon-interruption gaps honestly.

Collect declared reports/traces/screenshots and resolved non-secret provenance
before disposable teardown. Retain a result containing terminal job exit status,
cancellation state, infrastructure failures, evidence failures, cleanup failures,
URLs, and artifact locations. Any orchestration/collection/cleanup failure makes
the CLI result nonzero, while preserving the original job status separately.

`--keep-on-failure` retains remaining resources after a failed disposable run,
including cancellation, until explicit destroy or an explicit destructive
retention action. It does not resurrect crashed services. Mark retained instances
clearly and expose cleanup commands. Successful runs still clean up.

Bound log/artifact retention with configurable age and size limits. Never prune
active instance evidence or delete retained runtime/data as a side effect of log
pruning. Make expired evidence discoverable as expired rather than silently absent.

Human output and `--json` use the same operations. Finite JSON commands emit one
typed result on stdout; diagnostics use stderr. Following logs emits typed JSON
records in JSON mode. Provide explicit access to declared fixture identities and
sensitive values; ordinary status must not print secrets. Backlot's own events
must not serialize secret values; application stdout/stderr is captured as emitted.

## Out of scope

Frontend UI; remote/multi-host orchestration; Linux/Windows release support;
toolchain/version-manager installation; automatic watch/build/restart; arbitrary
job retriggering; shared-database provisioning; Compose import/export; CI scene
execution guarantees; DNS/router management; certificate provisioning/local trust;
raw Caddyfiles; generated application config; atomic multi-service rollouts;
transparent continuation across daemon crashes.

Existing CI paths may remain separate. Add Backlot CI execution only when it
materially reduces project scaffolding; V1 does not promise configuration parity
or require Docker-only scenes as a future CI boundary.

## Release acceptance

Demonstrate these behaviors using generic fixtures on macOS ARM64:

1. All four workflows run with one command, including focused test selectors.
2. Two worktrees, development and production scenes, and concurrent disposable
   runs coexist with distinct origins and private mutable resources. Declared
   shared-output groups prevent build races.
3. Repeated/concurrent persistent starts are idempotent. Stop/start and restart
   preserve mutations; reset removes them; tests never mutate persistent previews.
4. Fresh-only initialization is recorded only after success. Failed preparation,
   failed probes, and unexpected exits do not report a ready scene.
5. Existing trusted HTTPS works from a host browser and containerized server/test
   client, including API routing, SSR, redirects, cookies, and WebSockets.
6. Test failure, cancellation, client loss, graceful daemon shutdown, and crash
   recovery retain truthful results and never delete another instance's resources.
7. Port conflicts and unrelated Docker/Caddy resources survive startup, cleanup,
   reset, and recovery tests untouched. Unknown ownership is preserved.
8. Live and historical logs work after teardown; declared evidence survives it.
   Injected collection/cleanup failures cannot yield success. Keep-on-failure is
   inspectable and explicitly destroyable.
9. CLI/server share named Go wire types and have contract tests. JSON output,
   diagnostics, secret access, and missing-provider errors are predictable.
10. Adoption fixtures need a manifest, optional static seeds, and application
    commands/preparation code, with no copied port/ownership/cleanup harness.

Run the complete acceptance set before declaring V1 shipped. Compilation on
another OS, unit tests, or migration of one application are insufficient evidence.
