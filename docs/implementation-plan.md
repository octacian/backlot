# V1 implementation plan

The [PRD](prd.md) is the acceptance contract. Implement in vertical, reviewable
steps, keeping each milestone usable and tested. This repository's initial commit
is a scaffold, not the first completed runtime milestone.

## 1. Contracts and plan validation

Implemented for independent review: offline `backlot plan`, shared versioned
contracts, strict decoding/validation and generic adoption/fullstack fixtures.
See the [manifest guide](manifest.md). The schema ADR is accepted; this milestone
does not implement runtime execution.

Define the first manifest schema and machine configuration with typed references,
strict YAML/JSON decoding, scene lifetimes, component selection, dependency gates,
fresh-only/each-start job policy, output concurrency groups, environment layering,
and owned resources. Define concrete CLI target/argument parsing and API results.

Add a generic adoption fixture and a realistic production/dev/test manifest.
Exercise shared definitions, unknown fields, missing tools/inputs, graph cycles,
invalid resource references, secrets, and environment precedence. Read seed files
without shell execution or checkout writes. Do not pull in an application-specific
framework. The accepted schema ADR records these decisions.

Milestone 1 editor follow-up: committed project and machine JSON Schema artifacts
are generated from `api/v1` with deterministic drift checks and validated example
YAML. See [editor setup and limits](../schemas/README.md); this does not change
runtime acceptance or implement later milestones.

## 2. Local daemon and durable identity

Implemented for independent review: explicit background/foreground daemon control,
permissioned local typed API/client, local doctor, bbolt state, immutable private
snapshots, checkout/operation identity, metadata-only prepare/inspect/cancel/renew,
and bounded disposable leases. See the [daemon guide](daemon.md). `prepared` never
claims execution/readiness; native/provider behavior remains milestone 3 onward.

Implement daemon serve/start/stop/status behavior, the permissioned socket,
single-instance locking, typed versioned client/server, and machine `doctor`.
Explicit startup/setup is sufficient; no system service installer is required.

Implement transactional state, instance/operation records, checkout identity and
manifest snapshots, cancellation and disconnect leases. Test two real worktrees,
path aliases, duplicate concurrent starts, incompatible API/state versions, and
daemon shutdown. Prove allocation-intent recovery before external resources arrive.

## 3. Native vertical slice

Implemented for independent review: shared native run/restart/stop/log contracts,
daemon-owned guardian activation, TCP/HTTP/command readiness, finite dependency
jobs, deadlines including zero/unlimited, lease cancellation, durable logs and
crash reconciliation. Commands and descendants must remain in their supervised
group; see the [native guide](daemon.md#native-execution) and
[supervision decision](adr/20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md).
This milestone does not implement containers, publication, non-port resources,
fresh-only jobs, artifact extraction, keep-on-failure or retention policy.
Independent reviews and required final acceptance/CI remain gates.

Run a native persistent fixture and finite test job end to end. Add probe deadlines,
port-conflict handling, explicit restart, process-tree cancellation, serialized
lifecycle operations, and owned log capture with historical/follow APIs.

Prove child processes cannot outlive an acknowledged successful stop. Exercise
startup interruption, unexpected exits, parent/daemon crashes, and uncertain
ownership. Implement clear interrupted/failed states rather than auto-restart.

## 4. Generic containers and private state

Implemented for independent review: official Docker Go client lifecycle, private
networks/ports/storage/secrets, fresh-only completion records, persistent stop/run
and restart, explicit reset/destroy, conservative pre-stop drift rejection, durable
container logs and capability-based recovery. See the [runtime guide](containers.md).
Provider integration is an explicit opt-in suite; final independent reviews and
frozen-candidate checks remain gates. Publication and the broader milestone 6
evidence/retention features remain deferred.

Add the official Docker client adapter for containers, per-instance networks,
ports, and volumes; delegate image building to existing Docker tooling. Implement
private directory/volume/secret resources and use a MariaDB image as a generic
fixture. Provision application credentials through the image's declared settings.

Implement initialization and each-start jobs, persistence, reset, destroy, and
manifest-drift handling. Test simultaneous runs/worktrees, partial provisioning,
initialization failures, interrupted cleanup, output concurrency groups, and
preservation of unrelated resources. Never introduce SQL-aware provisioning here.

### Foreign bind cleanup policy

Implemented for independent review: authenticated directory ownership permits
cleanup despite unknown foreign bind identities by default. Positive overlaps
through any viable raw or verified Desktop identity still block deletion, and
ordinary project mounts/private-state validation remains strict. See the
[policy decision](adr/20261019T023642577Z-authorize-owned-directory-cleanup-despite-unknown-foreign-binds.md)
and [runtime risk and diagnostics](containers.md#ownership-and-recovery).

The early owned Docker stale declaration/native directory job checkpoint precedes
broader regression expansion. Focused unit/provider coverage exercises unknown
classification, independent candidates, known consumers, disposable exits, empty
reset generations, exact-ID retries and ownership/path preservation. Independent
Low/High reviews, frozen complete provider gates, actual supported Linux host
validation and exact published-revision CI remain required before readiness.

## 5. Published HTTPS and mixed execution

Implemented for independent review: configured existing Caddy JSON subroute
publication with optimistic concurrency, private intent/effect recovery, stable
hostnames and mixed upstream/client contexts. See the [HTTPS guide](https.md).
Owned real-gateway fixture evidence and real-domain trusted-certificate acceptance
are separate gates; independent reviews and final frozen-candidate checks remain
required before readiness. External DNS/certificates/trust remain operator-owned.

Add the Caddy adapter for a bounded, owned route scope, unique stable hostnames,
path routing, and concurrent configuration edits. Missing Caddy blocks only scenes
that publish an origin. Add host-native and containerized consumers behind the
same trusted origin, including SSR and WebSockets.

Keep automated tests on owned fixture gateways. Separately validate the real
owned-domain/trusted-certificate workflow on macOS ARM64. Record any manual DNS/TLS
setup and evidence honestly; mocked HTTP tests do not satisfy this gate.

### Managed native local HTTPS

Implemented for independent review: explicit managed/external configuration and
legacy external defaults, daemon gateway start/stop/restart/status, private CA and
saved configuration, loopback `.backlot.localhost:8443` origins, authenticated
native guardian lifecycle and exact-route crash recovery. Stock Caddy handles
local wildcard issuance/renewal with automatic trust installation disabled.
The [superseding gateway ADR](adr/20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md)
reconciles the former external-only decision. See [HTTPS operation](https.md).

Owned native fixtures cover CLI-to-daemon-to-Caddy, normal host DNS with private
client trust, paths/SSR/WebSockets/redirects/cookies, lifecycle conflicts, retained
origins/data, actual worktree isolation and surviving/unavailable gateway recovery.
These are fixture evidence, not system/browser trust acceptance. Independent
reviews, complete frozen regression suites and exact-HEAD CI remain gates. Trusted
browser/clean-machine macOS evidence is blocked pending a disposable environment;
no browser-warning bypass or persistent trust mutation can replace it.

The [recovery decision](adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
adds boot provenance, explicit force-stop waivers and guardian-liveness cleanup;
[operations](https.md#managed-gateway-recovery) retain CA/configuration and truthful
collection gaps.
The managed origin also exposes `ca_cert_path` for explicit native environment
assignments, with symbolic offline planning and validated existing public-root
resolution. See [application trust](https.md#explicit-application-trust). The
[runnable macOS quickstart](../examples/macos-quickstart/README.md) supplies
frontend/API/native SSR and disposable tests, explicit project trust, two-worktree
commands and scoped recovery/cleanup. Its owned complete suite is separate from
real operator/browser adoption and published-HEAD CI evidence; see
[testing](testing.md#runnable-macos-quickstart-integration).

### Managed container HTTPS consumers

Implemented for independent review in issue 21: explicit per-component
`host: host-gateway` mappings and read-only `origin_ca_cert` file mounts on macOS
ARM64 Docker Desktop Linux bridges, with `ca_cert_path` resolving to the chosen
container path only in explicit assignments. Engine expands the token at each
container create; no resolver helper or listener widening is needed. See the
[container extension decision](adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md)
and [runnable Go/Alpine example](../examples/container-https/README.md).

Owned fixture coverage verifies correct/missing/wrong trust, native frontend/API
and container SSR/test, variable WebSockets, redirects/cookies, peer isolation,
no Docker access for unselected host-only work, mapping/mount drift, retained
state, fresh creates across lifecycle and journaled crash recovery. This is
provider fixture evidence; independent low and fresh high reviews, complete
frozen regression suites and exact published-HEAD CI remain outstanding gates.

## 6. Disposable results and evidence

Implemented for independent review: terminal argument forwarding and exact exits,
separate execution/collection/cleanup/cancellation outcomes, signal and lease
cancellation, native/container artifact collection, kept failures and explicit
fixture secrets, retained historical/follow logs and daemon-wide evidence pruning.
See the [evidence guide](evidence.md). Focused native and provider fixtures exercise
failure and recovery. Independent reviews, frozen full suites and applicable
real-domain acceptance remain gates; this is not final V1 readiness.

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

## Linux platform expansion (issue 23)

Implementation adds explicit managed IPv4 HTTPS reachability, Linux amd64
reproducible builds/notices, Ubuntu 24.04 runtime/provider/browser CI and
[Linux/WSL operations](platforms.md). The
[Linux gateway decision](adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
retains private administration and explicit host-gateway/public-root trust.
Debian 13.4 amd64 actual-host evidence, complete frozen provider/native/macOS
regressions, independent reviews and exact published-HEAD CI remain required
before readiness. WSL is configuration guidance only, without WSL QA or guarantees;
compile-only CI does not establish support. Native Windows is now planned separately
under independent epic #34; it is not evidence delivered by the Linux expansion.

## Application scenes in CI (issue 22)

The [Ubuntu example](../examples/ci-scenes/README.md) adds a native directory-backed
application and disposable Playwright Chromium scenes, with explicit job-local
Caddy trust and an automatic two-shard matrix. The application owns test-worker
data isolation; Backlot retains lifecycle, checkout output locking and evidence
ownership. The initial Ubuntu Chromium checkpoint passed at `1fef82b`; see the
example's revision-bound evidence link. Lifecycle/isolation acceptance, independent
reviews and frozen published-HEAD gates remain required before readiness.

## Native Windows expansion (independent epic 34)

[Design slice #36](https://github.com/octacian/backlot/issues/36) supplies the
[Windows implementation handoff](windows-design.md), two Windows ADRs accepted
on 2026-10-18 following maintainer review, and an acceptance matrix. These
Windows-only decisions establish the implementation direction without superseding
existing Unix supervision/HTTPS decisions or claiming runtime support. This slice
contains no runtime or CI implementation.

[#37](https://github.com/octacian/backlot/issues/37) implements native Windows
private IPC/ACLs/identity, authenticated job supervision, Windows command semantics,
leases/results/recovery and real native runtime CI. It must first prove an actual
Windows CLI/daemon/native-app success and failure/recovery flow. Native-only evidence
does not complete the epic.

[#38](https://github.com/octacian/backlot/issues/38) integrates explicit Docker
Desktop Linux named pipes/mount ownership, stock native Caddy/persistent CA,
canonical host/Edge/native/container DNS/TLS and the owned Windows runnable example.
It adds provider CI and actual Windows 11 desktop trust/adoption evidence, including
correct/missing/wrong trust, recovery and unrelated-resource preservation. Both
implementation slices require independent review, frozen applicable regression
checks and exact published-HEAD CI. Runtime execution is expected directly on the
user's Windows VM with separately granted resources; WSL and cross-builds cannot
substitute. Release artifact work coordinates with #25 after verification.

## Scoped diagnostic follow-up (#26)

Implemented for independent review: selected-scene and recorded-instance doctor,
shared validated request/response/status contracts, explicit defining/storage/public
CA paths with launch versus current-file provenance, relevant read-only Docker and
gateway/listener/certificate/host DNS/TLS/upstream observations, and explicitly
opted-in bounded owned container probes. Host-only selection avoids Docker and
offline planning stays provider/mutation-free. Probe ownership is journaled before
effects, cancellation/shutdown joins owners and recovery preserves uncertainty.
See [contracts and image requirements](daemon.md#scoped-provider-diagnostics).

Early real CLI/API/daemon/native/Docker/Caddy/data success, missing container trust
failure/recovery and preserved-data cleanup were exercised before broad regressions;
revision-bound private delivery evidence records fixture/tool/image dependencies.
See [state compatibility](containers.md#state-compatibility) for v8: previous main v6 upgrades in place with preserved records and data; v5/v7
and other unsupported formats are rejected unchanged. Independent reviews,
frozen provider/browser/macOS suites and published-HEAD CI remain required
before delivery readiness; no browser/application trust adoption is claimed.

## Portable run export (#41)

Initial implementation for review adds typed daemon-owned export, a versioned
intentional agent report, script-free local HTML, strict/explicit partial evidence,
optional indefinite/bounded observation and atomic no-replacement publication.
Actions run/quickrun and the existing scenes collector consume the public export
contract. Generic evidence safety is product logic; application format validation
remains in the application fixture, including PNG signatures and ZIP-contained
trace/network NDJSON assertions. Launch provenance captures an immutable Git commit
when available, independent of checkout ownership. Export fence acquisition
observes cancellation and bounded waits even during another snapshot or retention.
See [export semantics](evidence.md#portable-per-instance-export).

The early real Ubuntu ci-scenes success/failure/recovery, teardown/relocation and
browser preview precede broader feature regression development. Independent Low
and fresh High reviews, final frozen complete provider/browser/action gates and
published-revision CI remain required; this paragraph does not claim readiness.
Binary distribution/version tags remain #25; the direct v6-to-v8 state transition
is scoped by the configuration decision.

## Registered project configuration (#48)

Implemented for independent review: explicit local project setup, external private
registry/generations shared by Git common-directory identity and manifest path,
private-only input values and strict tools composition, atomic edits/removal repair, hidden terminal input,
local explicit reveal, private launch/current provenance and effective drift.
Daemon state advances directly from previous main v6 to v8 in place, with typed
validation, retained ownership/data and current-only legacy activation; old private generations require
explicit operator retention/erasure decisions. See the [setup guide](manifest.md#registered-private-project-configuration)
and [current decision](adr/20261021T001732173Z-register-private-project-configuration.md).
Independent implementation reviews, frozen full validation and published-HEAD CI
remain gates; this section does not claim final feature readiness.

## Recorded persistent-scene targeting (#47)

Implemented for independent review: daemon-owned `scene start/status/stop`,
recorded canonical checkout/project/scene selection without current-manifest
parsing for status/stop, nearest recorded project without outer fallback, typed
absence/idempotent stop and bounded sampled observation via `status ID --follow`.
Follow observes the named scene through absence/recreation, fences initial
checkout/daemon continuity; ID follow stays pinned for either lifetime and history.
Neither acquires lifecycle ownership. The accepted [targeting decision](adr/20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
and [command contract](daemon.md#named-persistent-scenes) define the behavior.
Early real CLI/daemon/native failure/recovery and peer preservation precede focused
regressions; independent implementation reviews, frozen complete applicable suites
and exact published-revision CI remain delivery gates. Attached owner #50 and
streaming/history #54 remain separate; no schema or dependency change is needed.
