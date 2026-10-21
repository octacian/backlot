# Local daemon and execution

The Windows equivalent is the [accepted design for #37](windows-design.md#windows-owned-runtime-37),
not an implemented daemon. Unix permissions, signals and socket commands below
describe the existing macOS/Linux boundary.

Milestone 2 adds explicit local daemon control and durable metadata preparation.
`prepared` means a validated snapshot was recorded. It never means an application
was executed, a resource was reserved, or readiness was observed. Native execution
and retained logs are documented below. See [containers/private state](containers.md)
for Docker execution and retained resource lifecycle; Publication and retained evidence are documented in the
[HTTPS](https.md) and [evidence](evidence.md) guides.

## Commands

Build with `make build`. Daemon flags precede positional arguments:

```sh
bin/backlot daemon start --state-dir /absolute/private/backlot --json
bin/backlot daemon status --state-dir /absolute/private/backlot --json
bin/backlot prepare --state-dir /absolute/private/backlot \
  --project /absolute/project --json test
bin/backlot inspect --state-dir /absolute/private/backlot --json INSTANCE_ID
bin/backlot renew --state-dir /absolute/private/backlot \
  --lease-token LEASE_TOKEN --json INSTANCE_ID
bin/backlot cancel --state-dir /absolute/private/backlot --json INSTANCE_ID
bin/backlot doctor --state-dir /absolute/private/backlot --json
bin/backlot daemon stop --state-dir /absolute/private/backlot --json
```

`daemon start` backgrounds this executable's `daemon serve` and waits for a typed
handshake. `daemon serve` runs in the foreground until SIGINT, SIGTERM or an API
stop request. `daemon stop` joins owned runtime cleanup before acknowledging
`stopping`; cleanup or collection failure returns nonzero. Socket removal marks
completed shutdown. There is no service installer. Use the same `--state-dir` on each command;
without it, the directory is `backlot/daemon` below Go's `os.UserConfigDir()`.
State storage in this milestone uses that explicit flag/default; the manifest's
machine `storage` retention settings remain intent for later evidence storage.
Unix socket paths have OS length limits: choose a short private directory if a
long configured path cannot bind.

The directory must be user-owned and private (0700); socket, database, lock and
startup log are private (0600). Unsafe existing permissions, symlinked final state
directories, and symlinked or wrong-type daemon files are rejected, not repaired.
Clients check the permission boundary before dialing. A single-daemon advisory lock
covers startup through database closure. Only a private owned socket with a proven
connection-refused result is removed as stale; live or uncertain listeners remain
untouched. The lock file remains after shutdown and is not a PID ownership claim.

Before `prepare`, `run`, `restart` or `reset`, the shared Go client checks current
API/state compatibility with a fresh bounded status request. A running older or
malformed daemon is rejected before scene mutation; no compatibility result is
cached. Historical reads and snapshot cleanup calls do not perform this
configuration preflight. For incompatible state, use the matching older binary
to inspect, clean up and shut down its daemon, then use a fresh `--state-dir` with
the new binary; preserve old state until cleanup is verified.

`prepare` discovers the project locally and sends an absolute path to the daemon.
The daemon reuses strict manifest validation/environment resolution. Provider
configuration and health are not required for metadata preparation, including
container/published scenes. Native executable availability and declared required
inputs/seeds are checked without running application commands. Inherited values
come from the daemon's environment, not from a later CLI invocation. To change
that environment, stop and explicitly start the daemon from the intended context.

Persistent duplicates converge on project + checkout + scene, including concurrent
requests and symlink aliases. Every disposable preparation gets a new instance and
operation ID. Canonical checkout identity combines filesystem identity for the
checkout and its Git directory (when present); two real Git worktrees differ.
Snapshot reads bind to canonical project/checkout identities captured before reading
manifest, configuration or seed inputs. Acceptance rechecks both identities and
paths; an in-flight replacement is rejected before any durable intent is recorded.
Branch/HEAD text is provenance, never ownership. A moved checkout or replaced
checkout path is rejected with a typed error, preserving old records; use a
separate checkout rather than implicitly adopting old state. Non-Git projects use
the project directory's filesystem identity.

Repeated persistent preparation reports manifest/config digest drift and returns
the original record without overwriting its snapshot, including cancelled or
interrupted records. Metadata preparation itself never restarts runtime; native restart is described
below. Reset/destroy are documented in the [private-state guide](containers.md);
checkout adoption remains deferred.
Inspection addresses a recorded instance ID and does not read a changed checkout.
Ordinary views contain only the redacted plan. The daemon stores the validated
manifest privately and resolved sensitive values in a separate protected database
bucket. Seeds and inherited/input values are retained as resolved values; snapshots
are not reconstructed from the redacted public plan or reread during recovery.
Allocation-dependent values stay symbolic; preparation does not generate resources
or secrets for future providers. Do not put credentials in command arguments,
identifiers or paths; use sensitive environment declarations.

## Cancellation and recovery

A disposable preparation returns a lease capability once. `inspect` never returns
it. Renew explicitly before expiry using `renew --lease-token`; neither inspection
nor keeping an HTTP connection open extends the deadline. The default deadline is
30 seconds from acceptance/renewal; `daemon start|serve --lease-duration 30s`
configures it (100ms through 24h). Expiry cancels the preparation and preserves its
record and snapshots. Cancellation is explicit and idempotent for either lifetime;
terminal records cannot be renewed or resurrected. Persistent preparation is owned
by the daemon once accepted and has no client lease.

Before snapshot recording, a transaction saves instance, operation, checkout and
allocation intent with an ownership token. A separate transaction records the
private snapshot, resolved secrets and effect, then completion marks `prepared`.
Interruption before or after the effect leaves recoverable records, never a false
success. Recovery preserves intent/effect details and marks unfinished preparation
`interrupted`. Metadata-only preparations have no external resources to reconcile.

Graceful shutdown joins handlers, interrupts unfinished preparations and active
disposable preparations, and preserves all records. Crash recovery does the same
at startup after obtaining the exclusive lock. Completed persistent metadata stays
`prepared` across shutdown/crash; its metadata preparation operation is complete,
with no claim of execution. No automatic adoption, deletion or application restart
occurs. Cancelled/interrupted results remain inspectable after daemon restart.

## Local API and doctor

The CLI uses the named `api/v1` requests, responses, statuses and error envelope
through `internal/client`. Administrative HTTP is available only on `daemon.sock`,
never a TCP/LAN listener. Send `X-Backlot-API-Version: v1` on every request, and
`Content-Type: application/json` for POST bodies. POST contracts also require
`api_version: v1`. Unknown/missing versions are rejected, independently of binary
version. State compatibility is checked before mutation; see the
[private-state guide](containers.md#state-compatibility) for the v7
format and rejection/cleanup rules.

| Endpoint | Named request | Named response |
| --- | --- | --- |
| `GET /v1/status` | None | `DaemonStatusResponse` |
| `POST /v1/doctor` | `DoctorRequest` | `DoctorResponse` |
| `POST /v1/instances` | `ListRequest` | `ListResponse` (at most 100 summaries) |
| `POST /v1/stop` | `ControlRequest` | `DaemonStatusResponse` (`stopping`) |
| `POST /v1/prepare` | `PrepareRequest` | `InstanceResponse` |
| `POST /v1/inspect` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/cancel` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/renew` | `LeaseRequest` | `InstanceResponse` |
| `POST /v1/run`, `/v1/restart`, `/v1/reset` | `RunRequest` | `InstanceResponse` |
| `POST /v1/runtime/stop` | `StopRequest` (force requires selected `expected_attempt`) | `InstanceResponse` |
| `POST /v1/destroy` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/logs` | `LogsRequest` | `LogsResponse` |

`list` uses `ListRequest` (`after_id`, `limit`; zero means default 100, maximum
100) and returns identity-ordered summaries with a `next_after_id` continuation.
Empty lists contain an empty array. Historical/destroyed targets remain discoverable.
`status` is a CLI alias for `inspect`. Every instance response removes snapshot
ownership tokens while preserving the private durable recovery authority. Stop
an old daemon before upgrading the binary; use a matching CLI/daemon contract.
Fixture discovery and explicit sensitive access use `FixturesRequest` through
`POST /v1/fixtures` and `/v1/fixtures/secret`; see [evidence](evidence.md).

Failures use `ErrorResponse` with safe code/field/message values and an HTTP error
status. Request/response documents are bounded to 1 MiB, with strict types, exact
field names, no duplicates, nulls, unknown fields or trailing input. Oversized
resolved public plans are rejected before recording intent. Future-compatible
versions require an explicit contract change; clients reject incompatible API/state
versions and invalid response identities/statuses/lifetimes.

`doctor` checks local directory/socket/database permissions, socket connectivity,
and API/state compatibility through the daemon. It never opens the database beside
the daemon. Failed local checks return nonzero with a typed diagnostic result.
Bare doctor leaves Docker/Caddy checks explicitly `deferred` and contacts neither.
See [scoped provider diagnostics](#scoped-provider-diagnostics) for explicit scene/instance checks. A daemon that rejects state at startup reports the actionable
startup error in foreground output or the private `daemon.log` for background start.


## Native execution

Build with `make build`, then explicitly start a private daemon as above. Native
commands and **all descendants** must remain in Backlot's supervised process
group, including readiness commands. Do not use setsid, change descendant groups,
or delegate work to an unrelated supervisor. Deliberate escape detection and
kernel containment are not provided. The [supervision ADR](adr/20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
explains the ownership and recovery boundary.

```sh
bin/backlot run dev --state-dir /absolute/private/backlot --project /absolute/project --json
bin/backlot run test --state-dir /absolute/private/backlot --project /absolute/project --json -- -run TestExample
bin/backlot logs --state-dir /absolute/private/backlot --json --follow INSTANCE_ID
bin/backlot logs --state-dir /absolute/private/backlot --json --component server INSTANCE_ID
bin/backlot stop --state-dir /absolute/private/backlot --json INSTANCE_ID
bin/backlot restart INSTANCE_ID --state-dir /absolute/private/backlot --json
```

`run` and `restart` accept flags before or after the target. Arguments after `--`
are forwarded only to a disposable terminal job. The finite JSON result is one
`InstanceResponse`; the original terminal-job exit code is under
`instance.execution.components[].exit_code`. Any orchestration, cancellation,
collection or cleanup failure returns nonzero. Following logs emits one typed
`LogRecord` per NDJSON line, identifying instance, component, UTC collection time,
stream and execution attempt. Non-UTF8 chunks use the optional base64 `data`
field instead of text `message`, preserving raw bytes. Application stdout/stderr is captured as emitted;
do not write secrets to those streams. Logs persist outside runtime across stop
and restart. Artifact collection, keep-on-failure and configurable evidence retention are
documented in the [evidence guide](evidence.md).

Generic containers, private volumes/directories/secrets/networks and fresh-only
jobs follow the [private-state lifecycle](containers.md). Publication follows the
[HTTPS guide](https.md). Metadata `prepare` retains its allocation-free semantics for all
supported declarations. Native TCP/HTTP probes additionally require `lsof` on the
daemon PATH, to prove the listener belongs to the supervised group at the actual
dialed destination; an unrelated listener never satisfies
readiness and is never killed. Targets must resolve exclusively to loopback
addresses. Resolution is validated once per attempt and the owned numeric
endpoint is pinned for TCP and HTTP dialing, including HTTPS hostname checks.
Remote dependency probes are outside this native contract. IPv6 wildcard listeners
can satisfy IPv4 loopback probes only when read-only socket inspection proves
dual-stack capability: Darwin uses public process-fd socket information; Linux
uses the observed fd inode and kernel socket-diagnostic IPv6-only attribute.
This adds no accept requirement or runtime privilege. Unavailable or uncertain
socket information fails closed; IPv6-only listeners cannot satisfy IPv4 probes.
Assigned loopback ports are observed free rather than reserved. A proven collision
allows up to three allocation attempts, each after verified cleanup of the failed
owned group, while the affected port has no earlier consumer. Running services
and completed preparation jobs keep their consumed allocations: a later conflict
on one of those ports fails the scene with verified cleanup rather than moving
the port or replaying work. Other service/job failures are never automatically retried.

`--startup-timeout` defaults to `5m` and bounds dependency startup and each-start
preparation jobs. `--job-timeout` defaults to `30m` for each job; the disposable
terminal job receives its own job budget after dependencies are ready.
Manifest readiness `timeout` adds a probe budget. Each execution/probe budget can
be `0s` for unlimited; any other applicable finite budget still limits execution.
Negative durations are rejected. API execution options use the same duration
strings. `--stop-grace` accepts any finite nonnegative duration and defaults to `10s`;
zero means immediate escalation. Verification and collection have separate finite
budgets after the configured grace. The outer owner join permits the accepted
grace plus 20 seconds of bounded activation, verification and collection overhead;
a failed join reports failure and retains state ownership until the owner exits.
Disposable clients renew the finite daemon
lease while waiting; unlimited execution never disables lease cancellation.
Expired runs receive independent cancellation coordinators, so one run's grace
or lifecycle lock cannot delay cancellation of another. Sweeps remain responsive
to later expiries and daemon shutdown; shutdown joins execution and cancellation
owners before releasing their store, or reports failure and retains ownership.

Persistent startup is daemon-owned after acceptance, so closing a client leaves
it running. Concurrent starts join the same instance, and repeated starts report
manifest/config drift without replacing the accepted private snapshot. Failed,
interrupted instances require explicit `restart`, which revalidates
checkout identity, applies compatible command/environment/graph/probe drift, and
retains logical identity. Resource contract changes are rejected before stopping
old work and require explicit `reset` or `destroy`. Stop/run reuses the retained
snapshot and storage; restart can apply safe drift. Reset removes the generation
and starts fresh under the same identity; destroy removes it and retains historical
results/logs. See the [private-state contract](containers.md#generations-jobs-and-drift)
for initialization, credentials, conservative drift and cleanup behavior.
Instance mutations and declared checkout-output groups serialize across
scenes/runs. Ctrl-C/SIGTERM during a CLI
run cancels and joins the supervised work. Ordinary persistent client disconnect
does not cancel accepted startup; disposable client loss expires its lease.

An outside guardian owns a separate anchor child in a distinct process group.
The guardian starts and waits for the application root in that anchor group,
reports its original result, and retains the anchor until group cleanup. It signals
only that pinned group and does not reap the anchor until no other member remains and the anchor has exited.
No observed member PID is signaled. After anchor join, a kernel signal-0 group
query must prove ESRCH; enumeration alone cannot acknowledge absence. Successful
stop through the guardian also requires joined log/status collectors, a private authenticated cleanup receipt, and verified guardian exit.
Control sockets and receipts live in random mode-0700 `/tmp/blctl-*` directories;
their paths and random capabilities stay in the private runtime journal. Missing
root PIDs, EOF and closed listeners do not establish cleanup. Graceful daemon
shutdown stops owned work. Recovery requests cleanup through the same authority
and marks interrupted with an explicit collection gap; it never resumes work.

All native launch intents/effects, including command probes, record kernel boot
identity before allocation. A different boot releases obsolete process claims,
never collection gaps. The guardian-only pipe lets a live anchor terminate its own
group after guardian death; it provides neither collection proof nor restart
eligibility. Root or anchor-only death remains cleanable by the guardian; daemon
loss leaves that authority available. Combined authority loss may leave survivors.

When ordinary stop cannot resolve ownership, restore authority, perform a controlled
reboot, or explicitly accept survivor risk with `stop --force INSTANCE` before
ordinary restart. Force attempts normal cleanup, then relinquishes only the selected
native claims: **unknown survivors may still run/write**. The CLI selects the current
attempt; API callers retain its `expected_attempt` through retries. Stale requests
fail before cleanup. Force is limited to stop; non-native fences and independent
absence proof before later data removal still apply. Private history and safe warnings survive replacement; see
[evidence semantics](evidence.md#investigation-and-destruction),
[gateway recovery](https.md#managed-gateway-recovery) and the
[decision rationale](adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md).

See [disposable results and evidence](evidence.md) for exact exit codes, kept
failures, artifacts, fixture access and retention settings.

The CLI waits for persistent ready or disposable terminal status through inspect;
raw API callers must renew disposable capabilities while polling. Logs pages use
an absolute unfiltered record offset, so component filtering preserves cursor
progress. Inspect and cancel remain shared across metadata and runtime execution.

## Managed gateway control

`backlot gateway start|stop|restart|status --state-dir ... --json` uses named
`GatewayRequest`/`GatewayResponse` contracts at `/v1/gateway/<action>` (POST).
Start accepts an absolute machine `config_path`; first start requires explicit
managed mode and an absolute native Caddy executable. Subsequent start/restart
may reuse the retained configuration; stop/status reject configuration arguments.
Responses omit guardian capabilities and CA private keys. Gateway process/listener
readiness is independent of client trust. See [HTTPS operations](https.md) for
separate stock trust installation, lifecycle fences and retained recovery state.

## Scoped provider diagnostics

Bare `backlot doctor` preserves daemon-only access checks. It shows state, socket,
log/evidence/data locations and the default machine-config candidate, explicitly
without loading a project or configuration. Select a scene or recorded instance:

```sh
backlot doctor --project /absolute/checkout --scene dev --config /absolute/machine.yaml --json
backlot doctor --instance INSTANCE_ID --json
```

Both accept the same `--state-dir`. `--scene` discovers a project if `--project`
is omitted. Project/config flags require a scene; `--instance` rejects all three.
Scene diagnostics validate selected tools, inputs, seeds and machine declarations,
then read only relevant providers. Host-only work does not contact Docker even
when the machine file or unselected declarations contain Docker settings.
Offline `plan` remains provider-free and mutation-free.

The named `DoctorRequest`/`DoctorResponse` contract serves `POST /v1/doctor`.
Decoded selections, enum outcomes and response consistency are validated. Each
check carries a context and `ok`, `failed`, `unsupported`, or `deferred` status.
Any failed check produces nonzero CLI exit. `healthy` means no observed failure;
it is not aggregate readiness and does not turn incomplete evidence into success.
Unavailable runtime ports/origins are explicitly deferred; doctor never starts an
application to obtain them. Checks distinguish configured Docker access, managed
saved configuration/pinned CA, Caddy API/scope/exact recorded route, TCP listener,
public CA and normally verified leaf/chain dates, normal host DNS/TLS, and actual
recorded TCP upstream reachability. An external gateway's filesystem and network
context remain operator-owned and cannot be inferred from daemon-host success.

Human output and JSON `paths` expose the selected manifest and effective machine
file. Instance output distinguishes `launch_manifest`/`launch_machine_config`
(recorded paths and digests) from `current_*` observations. Changed/missing current
files do not redirect recorded runtime diagnostics. Older snapshots without a
recorded config path say `unrecorded`; a recorded absent default says `absent` and
is not presented as a loaded config. State, socket, data, logs, artifacts, managed
saved configuration and the actual `public_ca_certificate` file are locatable.
A printed path may be absent/unavailable; its status is explicit.

For a managed gateway, `public_ca_certificate` is the actual public PEM root file
under private daemon state, usable for a separate explicit manual trust action.
No key path/content, provider credential, ownership token or application environment
is emitted. Preserve private state; do not publish raw database/log files.
`daemon/host system trust` and `daemon/host explicit public CA` are separate TLS
observations. Neither establishes browser or application trust. Doctor never
installs trust, changes DNS/resolver/router settings, repairs providers, replaces
foreign routes, or bypasses certificate/hostname verification.

### Explicit owned container probe

```sh
backlot doctor --instance INSTANCE_ID --container-probe-image local-diagnostics:tag
backlot doctor --instance INSTANCE_ID --container-probe-image local-diagnostics:tag --probe-public-ca
```

This is explicit authorization for a temporary owned diagnostic bridge/container,
not an application launch. The image must already exist at the recorded local
Docker endpoint; Backlot inspects and pins its image ID and never pulls/builds it.
The image contract requires executable `/bin/sh`, `/bin/bash`, `getent hosts`,
GNU-compatible `timeout` (exit 124 on timeout), and OpenSSL `s_client` supporting
`-connect`, `-servername`, `-verify_hostname`, `-verify_return_error` and `-CAfile`.
For example a preexisting Debian-based `golang:1.27.1` image provides these tools;
operators select their own compatible image. Missing tools are `unsupported`.
Images declaring volumes are unsupported. Image/app entrypoint and command are
replaced by the bounded probe script and image healthchecks are explicitly disabled;
scene environment and workloads are not run.

The probe resolves the canonical hostname normally, checks TCP with bounded Bash
`/dev/tcp`, then verifies SNI, hostname, chain and dates using OpenSSL. A selected
managed consumer's declared origin `host-gateway` mapping is preserved verbatim;
no IP inference, resolver helper or TLS bypass is added. Default image trust and
explicit public-CA trust are separate invocations. `--probe-public-ca` mounts only
the actual public root file read-only at `/backlot-public-ca.crt`; it never mounts
keys/directories or changes image/system trust. TCP/TLS failures remain distinct
from DNS failures; none establishes other application images' trust. Curl clients
can have different `.localhost` resolution behavior and are not this probe contract.

Execution is bounded to ten seconds (within the overall fifteen-second observation
budget); independent cleanup has twenty seconds. The root filesystem is read-only,
capabilities are dropped, privileges cannot escalate, memory/process counts are
bounded, and stdout/stderr are discarded. No command/provider output is exposed.
The probe runs as container root so it can read the existing mode-protected public
root without weakening host permissions. No application mounts, secrets or
unrelated network attachments are provided.

Ownership intent is persisted before each create; exact capability labels and
engine IDs gate activation/removal. Cancellation and daemon shutdown join the
probe before storage ownership is released. Recovery reconciles exact owned effects,
resolving lost create responses by name only to authenticate the ownership label
and capture the full Engine ID. All subsequent cleanup requests use that ID;
disappearance never permits falling back to a replacement under the same name.
Cleanup failure is nonzero and retains private journals/uncertain resources;
restore the recorded
provider access and restart the daemon to retry recovery. Foreign/mismatched
ownership is preserved. Existing images and global settings remain untouched.

## Configuration state compatibility

Registered private project composition uses daemon state version 7. State v5/v6 is
rejected without migration or mutation: use its matching binary to clean up and
shut down owned resources, then select a fresh state directory. The independent
project registry survives daemon reset and state-directory replacement. See
[private setup, provenance and retention](manifest.md#registered-private-project-configuration).
