# Architecture

The executable implements version discovery, offline scene planning, and a local daemon
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
separate from API types. See the [daemon guide](daemon.md#local-api-and-doctor)
for endpoint and transport details.

The lifecycle coordinator consumes a validated, resolved plan and runs its graph.
It owns idempotency, instance mutation locking, dependency gates, initialization
records, cancellation, evidence, and teardown ordering. It calls narrow adapters
for native work, containers, gateway mutations, storage, and probes.

Native application adapters own cooperative sessions through live authenticated
guardians; uncertain ownership remains fenced. See the
[native supervision decision](adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md).
Docker adapters use the official Go client for lifecycle/inspection and established
Docker tooling for image builds. Gateway adapters use Caddy's HTTP API and owned
route IDs/scope, including concurrency preconditions rather than whole-config
replacement.

## Shared API

`api/v1` is the source of truth for requests, responses, errors, status enums, and
streamed events. CLI client and daemon handlers marshal/unmarshal these same
named Go types. Internal state models remain private. Offline planning does not
require a daemon or provider contact.

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

## State and recovery

Only the daemon opens the bbolt state database; mutations use transactions.
Versioned state retains instance/resource IDs, allocation intent, ownership tokens,
resolved plans, preparation completion, attempts and results. Supported migrations
run in one startup write transaction; unsupported formats are rejected unchanged.
See [state compatibility](containers.md#state-compatibility) and the
[migration decision](adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).

Use protected per-user state directories and secret storage. Secrets may be
persisted for resource reuse but never appear in ordinary snapshots/results.
Keep logs/artifacts in separately retained files with restrictive permissions.
[Declared startup disclosure](adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
uses dedicated sensitive retrieval and transient output, never persisted public results.

An external engine call and a database transaction are not atomic together.
Record intent before allocation, label/identify engine resources, then record the
effect. Recovery reconciles those records with observed resources; it must never
infer ownership from a familiar name alone. Keep unknown ownership visible and
untouched. Do not hold storage transactions open over engine/network operations.

## Dependencies and platform policy

The executable uses urfave/cli v3, Go's HTTP/JSON/slog/exec libraries, bbolt,
YAML v3, and maintained clients for external engines. See [Go practices](go-practices.md)
and the [dependency policy](contributing.md) for contributor expectations.

Platform-specific supervision and socket operations remain separate from domain
code. The [platform policy](platforms.md) defines current targets and evidence
limits. Native Windows releases and runtime support are outside project scope;
the [Windows design](windows-design.md) is retained as historical reference.

## Configuration and routing

The versioned [manifest](manifest.md) defines scenes and typed references in YAML
or JSON. Private [project configuration](manifest.md#registered-private-project-configuration)
and machine configuration supply tools, inputs and provider settings.

Projects request routes and explicitly map resolved connection values into their
consumers. Caddy mode selects daemon-managed local-CA HTTPS or externally managed
DNS/TLS and gateway lifecycle. Trust setup is an explicit operator action.
Native and container consumers have distinct reachability and certificate paths;
see [HTTPS](https.md) for gateway ownership, configuration and platform boundaries.

## Shared-service allocation boundary

Separate project server and instance allocation journals preserve identity and
recovery independently of checkout/private generations. The daemon owns intent,
credentials, exact configuration, locking and consumer ordering; `internal/manager`
owns bundled MariaDB/PostgreSQL create/inspect/release SQL effects. Server volume/network/container
effects reuse the official Docker adapter. Named `api/v1` types serve CLI/client/
daemon control; [manager messages](../api/v1/manager.go) remain private.
The [accepted decision](adr/20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
defines lifecycle and bounded transient force; the additive
[PostgreSQL decision](adr/20261025T202152388Z-add-bundled-postgresql-shared-allocations.md)
defines its driver, ACL, atomic witnesses and bootstrap/recovery boundaries. The [runtime guide](containers.md#project-scoped-shared-services)
defines connection semantics and [operator recovery](containers.md#explicit-operator-force-cleanup).
