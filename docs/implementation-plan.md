# V1 implementation plan

The [PRD](prd.md) is the acceptance contract. Implement in vertical, reviewable
steps, keeping each milestone usable and tested. This repository's initial commit
is a scaffold, not the first completed runtime milestone.

## 1. Contracts and plan validation

Implemented for independent review: offline `backlot plan`, shared versioned
contracts, strict decoding/validation and generic adoption/fullstack fixtures.
See the [manifest guide](manifest.md). The schema ADR remains proposed until
review acceptance; this milestone does not implement runtime execution.

Define the first manifest schema and machine configuration with typed references,
strict YAML/JSON decoding, scene lifetimes, component selection, dependency gates,
fresh-only/each-start job policy, output concurrency groups, environment layering,
and owned resources. Define concrete CLI target/argument parsing and API results.

Add a generic adoption fixture and a realistic production/dev/test manifest.
Exercise shared definitions, unknown fields, missing tools/inputs, graph cycles,
invalid resource references, secrets, and environment precedence. Read seed files
without shell execution or checkout writes. Do not pull in an application-specific
framework. Finalize schema decisions in an ADR.

## 2. Local daemon and durable identity

Implement daemon serve/start/stop/status behavior, the permissioned socket,
single-instance locking, typed versioned client/server, and machine `doctor`.
Explicit startup/setup is sufficient; no system service installer is required.

Implement transactional state, instance/operation records, checkout identity and
manifest snapshots, cancellation and disconnect leases. Test two real worktrees,
path aliases, duplicate concurrent starts, incompatible API/state versions, and
daemon shutdown. Prove allocation-intent recovery before external resources arrive.

## 3. Native vertical slice

Run a native persistent fixture and finite test job end to end. Add probe deadlines,
port-conflict handling, explicit restart, process-tree cancellation, serialized
lifecycle operations, and owned log capture with historical/follow APIs.

Prove child processes cannot outlive an acknowledged successful stop. Exercise
startup interruption, unexpected exits, parent/daemon crashes, and uncertain
ownership. Implement clear interrupted/failed states rather than auto-restart.

## 4. Generic containers and private state

Add the official Docker client adapter for containers, per-instance networks,
ports, and volumes; delegate image building to existing Docker tooling. Implement
private directory/volume/secret resources and use a MariaDB image as a generic
fixture. Provision application credentials through the image's declared settings.

Implement initialization and each-start jobs, persistence, reset, destroy, and
manifest-drift handling. Test simultaneous runs/worktrees, partial provisioning,
initialization failures, interrupted cleanup, output concurrency groups, and
preservation of unrelated resources. Never introduce SQL-aware provisioning here.

## 5. Published HTTPS and mixed execution

Add the Caddy adapter for a bounded, owned route scope, unique stable hostnames,
path routing, and concurrent configuration edits. Missing Caddy blocks only scenes
that publish an origin. Add host-native and containerized consumers behind the
same trusted origin, including SSR and WebSockets.

Keep automated tests on owned fixture gateways. Separately validate the real
owned-domain/trusted-certificate workflow on macOS ARM64. Record any manual DNS/TLS
setup and evidence honestly; mocked HTTP tests do not satisfy this gate.

## 6. Disposable results and evidence

Complete terminal-job selection, argument forwarding, exact exit-status capture,
SIGINT/SIGTERM behavior, disconnect cancellation, failure-triggered cleanup,
artifact extraction, `--keep-on-failure`, fixture discovery/explicit secret access,
and bounded log/artifact retention.

Inject job, provider, collection, and cleanup failures. Verify independent result
fields and nonzero overall status. Retrieve logs after containers are deleted;
destroy retained failures without deleting retained evidence prematurely.

## 7. Release acceptance and polish

Run every [release acceptance criterion](prd.md#release-acceptance) with generic
fixtures on macOS ARM64. Cover the development, manual-update production preview,
backend integration, and production UI integration workflows together.

Document installation, explicit setup, manifest/reference examples, CLI/API
semantics, logs, reset/destroy, and crash recovery using verified commands.
Provide a reproducible macOS ARM64 binary build and include required dependency
license notices. Measure cold/warm startup and adoption configuration size without
inventing unvalidated performance targets. Do not require any external project's
migration or offer CI orchestration as a release gate.

## Handoff instructions

Read the documentation index and relevant accepted ADRs. Begin with milestone 1;
do not assume the scaffold implements a daemon or a frozen manifest schema.
The existing references for repository conventions are the CLI action, shared
version response, ADR package, and their tests. Introduce runtime packages as
their behavior is implemented; do not prebuild a provider/plugin framework.

The hard work is ownership, interruption, dependency readiness, and truthful
cleanup/results. Prefer maintained libraries for solved infrastructure concerns
and write the smallest Backlot-specific lifecycle code that preserves the PRD.
Keep the implementation plan current as milestones land. Any material scope
change needs an explicit decision rather than silently weakening acceptance.
