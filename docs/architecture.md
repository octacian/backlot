# Architecture

This is the implementation baseline for the [V1 PRD](prd.md). Runtime components
are planned; the current scaffold contains only the executable and version
contract plus contributor tooling.

## Boundaries

One executable exposes the human/agent CLI and daemon commands. The daemon is the
single owner of machine state and runtime mutations. The CLI is a typed client;
it must not independently allocate ports, mutate Caddy, or clean up Docker state.

Use a per-user permissioned Unix socket for local HTTP/JSON. Socket placement,
single-daemon locking, and stale-socket recovery belong in the platform/runtime
adapter. Do not expose administrative HTTP on the LAN. Keep transport selection
separate from API types for eventual native Windows support.

The lifecycle coordinator consumes a validated, resolved plan and runs its graph.
It owns idempotency, instance mutation locking, dependency gates, initialization
records, cancellation, evidence, and teardown ordering. It calls narrow adapters
for native work, containers, gateway mutations, storage, and probes.

Native adapters own complete process trees, not only launcher PIDs. Docker adapters
use the official Go client for lifecycle/inspection and established Docker tooling
for image builds. Gateway adapters use Caddy's HTTP API and owned route IDs/scope,
including concurrency preconditions rather than whole-config replacement.

## Shared API

`api/v1` is the source of truth for requests, responses, errors, status enums, and
streamed events. CLI client and daemon handlers marshal/unmarshal these same
named Go types. Internal state models remain private. The scaffold begins with
`VersionResponse`; add actual endpoint contracts alongside their implementations.

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
currently a release candidate. Reassess its stable release before implementing
the manifest parser. Do not add unused runtime dependencies to the scaffold.

Keep platform-specific supervision and socket operations separate from domain
code. macOS ARM64 is the V1 runtime acceptance platform. Linux CI and Windows
cross-builds catch portability issues without promising runtime support there.

## Configuration and routing design work

Implement one versioned manifest model for both YAML and JSON. Reference namespaces
must be bounded and typed (instance, resources, services, and declared outputs),
not a general template language. Freeze the initial concrete schema during the
first implementation milestone and exercise it with generic fixtures.

Machine setup explicitly records Docker access, Caddy admin endpoint/owned scope,
domain suffix, and the host address reachable by a containerized gateway. Do not
assume loopback means the same thing inside Docker. Projects request origin/path
routes and map resolved values into consumers' environments.

Caddy's TLS and DNS setup is external. Validate the shared origin through HTTPS
from every required consumer context. Keep listener readiness separate from final
public-origin readiness to avoid proxy/SSR dependency cycles.
