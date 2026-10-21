# Architecture

This is the implementation baseline for the [V1 PRD](prd.md). The executable
implements version discovery, offline scene planning, and a local metadata daemon
with transactional preparation/recovery, native and container execution, and retained
private resource generations and managed/external HTTPS publication. See the [HTTPS guide](https.md), [manifest guide](manifest.md) and
[daemon guide](daemon.md).

## Boundaries

One executable exposes the human/agent CLI and daemon commands. The daemon is the
single owner of machine state and runtime mutations. Runtime CLI actions use the
typed local client; they must not independently allocate ports, mutate Caddy, or
clean up Docker state.

Use a per-user permissioned Unix socket for local HTTP/JSON. Socket placement,
single-daemon locking, and stale-socket recovery belong in the platform/runtime
adapter. Do not expose administrative HTTP on the LAN. Keep transport selection
separate from API types. The [accepted Windows design](windows-design.md) specifies named
pipes and Windows supervision for #37/#38; current runtime support is unchanged.

The lifecycle coordinator consumes a validated, resolved plan and runs its graph.
It owns idempotency, instance mutation locking, dependency gates, initialization
records, cancellation, evidence, and teardown ordering. It calls narrow adapters
for native work, containers, gateway mutations, storage, and probes.

Native adapters own complete cooperative process groups, including descendants,
not only launcher PIDs. Commands must not detach or delegate work outside that
group; see the [native supervision decision](adr/20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md). Docker adapters
use the official Go client for lifecycle/inspection and established Docker tooling
for image builds. Gateway adapters use Caddy's HTTP API and owned route IDs/scope,
including concurrency preconditions rather than whole-config replacement.

## Shared API

`api/v1` is the source of truth for requests, responses, errors, status enums, and
streamed events. CLI client and daemon handlers marshal/unmarshal these same
named Go types. Internal state models remain private. The executable uses
`VersionResponse` and named planning request/result/error contracts; add actual endpoint contracts alongside their implementations. Offline
planning does not imply an HTTP endpoint is available.

Do not use stringly typed maps or duplicate anonymous structs for wire payloads.
Use typed nested values and explicit discriminators when needed. Validate decoded
fields, bound request bodies, reject trailing input, and make enum/reference
validation explicit. Shared Go types prevent field drift, not invalid JSON input
or incompatible versions. Contract tests must cover both encode/decode directions,
error envelopes, and stream framing.

Use a `/v1` endpoint namespace, negotiate/reject incompatible API versions clearly,
and return structured error codes with actionable messages. Give long operations
stable operation/instance IDs; use explicit cancellation and bounded disposable
client leases rather than tying all work to a single HTTP connection. JSON log
following uses newline-delimited typed records. Final run results remain available
after cancellation or reconnection.

Future UI adapters use this API. Do not add a second lifecycle implementation in
the UI or CLI. A separately published Go SDK is not required for V1.

## State and recovery

Recommended embedded storage is bbolt: only the daemon opens the database and
mutations use transactions. Keep a versioned state format; reject newer unsupported
formats. Store instance/resource IDs, allocation intent, ownership tokens,
resolved plan snapshots, preparation completion, attempts, and results.

Use protected per-user state directories and secret storage. Secrets may be
persisted for resource reuse but never appear in ordinary snapshots/results.
Keep logs/artifacts in separately retained files with restrictive permissions.

An external engine call and a database transaction are not atomic together.
Record intent before allocation, label/identify engine resources, then record the
effect. Recovery reconciles those records with observed resources; it must never
infer ownership from a familiar name alone. Keep unknown ownership visible and
untouched. Do not hold storage transactions open over engine/network operations.

Before committing to the storage adapter, exercise intent/effect interruption and
state-version behavior. SQLite is a reasonable alternative if relational queries
would remove more code than bbolt; record that change in an ADR rather than
quietly adding a second state store.

## Dependencies and platform policy

Use urfave/cli v3 for commands, Go's HTTP/JSON/slog/exec libraries for their existing
capabilities, and maintained parsers/clients for external formats and engines.
The scaffold uses the stable maintained YAML v3 line for ADR metadata; YAML v4 is
still a release candidate after the milestone 1 reassessment. The manifest parser
reuses v3; see the
[accepted schema decision](adr/20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md).
Do not add unused runtime dependencies to the scaffold.

Keep platform-specific supervision and socket operations separate from domain
code. The [platform policy](platforms.md) targets macOS ARM64, Ubuntu 24.04 amd64
and Debian 13.4 amd64. Linux runtime support requires actual lifecycle, IPC,
permissions, trust and cleanup evidence plus Ubuntu runtime CI; compilation is
insufficient. WSL receives configuration guidance only. Native Windows is planned
under independent epic #34, with no runtime support claim before #37/#38 evidence.

## Configuration and routing design work

Implement one versioned manifest model for both YAML and JSON. Reference namespaces
must be bounded and typed (instance, resources, services, and declared outputs),
not a general template language. Freeze the initial concrete schema during the
first implementation milestone and exercise it with generic fixtures.

Machine setup explicitly records Docker access, Caddy admin endpoint/owned scope,
domain suffix, and the host address reachable by a containerized gateway. Do not
assume loopback means the same thing inside Docker. Projects request origin/path
routes and map resolved values into consumers' environments.

Caddy mode is explicit; omitted mode preserves legacy external ownership. In
managed mode the daemon activates an explicitly configured stock native executable
through the existing journaled authenticated guardian. It owns explicitly configured HTTPS listeners,
private persistent CA/config state and `*.backlot.localhost:8443` origins; stock
Caddy performs local issuance/renewal. Trust installation is disabled; a separate
operator `caddy trust` targets the owned endpoint. Serialize gateway lifecycle with
scene acceptance, refuse stop/restart with active published scenes and fail port
conflicts without fallback. Recovery removes exact scene routes before authenticated
gateway stop, preserves uncertain intents and CA/data, and requires explicit start.
Only validated saved effective config can resume after first activation; missing
saved state or changed/unusable CA cannot fall back to bootstrap. See the
[gateway decision](adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md).

Container consumers explicitly map their attached published origin's canonical
hostname to the explicit Engine `host-gateway` token. Pass that token directly to
Docker ExtraHosts at each container create, including fresh creates on restart;
Engine owns address expansion. Do not infer a DNS name, look up host DNS, copy an
IP or allocate a resolver helper. The public-root `origin_ca_cert` mount is
a read-only file at the project's chosen absolute container target; explicit
container `ca_cert_path` environment references resolve there. Native references
retain the private host public-root path. Keys/CA directories are never consumer
mount inputs, trust is never injected implicitly, and runtime detection/TLS
bypasses are excluded. Managed HTTPS defaults to IPv4/IPv6 loopback for the macOS ARM64 Docker Desktop
topology. Rootful Linux bridges require an explicit numeric IPv4
`caddy.https_bind_address`; a specific address adds a listener, while `0.0.0.0`
replaces the IPv4 loopback socket. Administration always stays loopback; no
automatic widening or Docker address inference occurs. See the
[Linux gateway decision](adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md). Host-only selected work
does not access Docker; snapshots and journaled recovery retain their existing
ownership boundaries.

External Caddy retains operator TLS/DNS/lifecycle ownership. Validate the shared origin through HTTPS
from every required consumer context. Keep listener readiness separate from final
public-origin readiness to avoid proxy/SSR dependency cycles.

## Shared-service allocation boundary

Project-scoped server records and instance allocation records are separate from
checkout identity and private resource generations. A focused `internal/manager`
boundary owns MariaDB-specific create/inspect/release effects; the daemon owns
private intent, credentials, exact server configuration, locking, consumer ordering
and conservative recovery. Shared server volume/network/container effects use the
existing official Docker client and capability verification. The same named
`api/v1` contracts serve `shared` CLI/client/daemon control; manager messages are
private provisioning contracts and never public status. See the
[accepted decision](adr/20261020T233406000Z-allow-explicit-force-cleanup-of-recorded-shared-targets.md)
and [current bounded runtime](containers.md#project-scoped-shared-services-gate-1).

Explicit operator force is limited to instance/shared destroy and manager release.
It waives shared ownership proof for recorded targets, preserves peer/consumer and
native/private authority fences, and requires observed deletion/absence. Authority
is transient; recovery does not continue force. See the [operator procedure](containers.md#explicit-operator-force-cleanup).
