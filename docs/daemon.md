# Local daemon and native execution

Milestone 2 adds explicit local daemon control and durable metadata preparation.
`prepared` means a validated snapshot was recorded. It never means an application
was executed, a resource was reserved, or readiness was observed. Native execution and retained logs are documented below; Docker, Caddy, broader
resources and evidence retention remain later milestones.

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
stop request. `daemon stop` joins native cleanup before acknowledging `stopping`; cleanup or
collection failure returns nonzero. Socket removal marks completed shutdown. There is no service installer. Use the same `--state-dir` on each command;
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
below. Reset/destroy/adoption remain later operations.
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
version. State format 1 is checked before state mutation; unsupported/missing
formats in existing populated databases are rejected without migration.

| Endpoint | Named request | Named response |
| --- | --- | --- |
| `GET /v1/status` | None | `DaemonStatusResponse` |
| `POST /v1/stop` | `ControlRequest` | `DaemonStatusResponse` (`stopping`) |
| `POST /v1/prepare` | `PrepareRequest` | `InstanceResponse` |
| `POST /v1/inspect` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/cancel` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/renew` | `LeaseRequest` | `InstanceResponse` |

Failures use `ErrorResponse` with safe code/field/message values and an HTTP error
status. Request/response documents are bounded to 1 MiB, with strict types, exact
field names, no duplicates, nulls, unknown fields or trailing input. Oversized
resolved public plans are rejected before recording intent. Future-compatible
versions require an explicit contract change; clients reject incompatible API/state
versions and invalid response identities/statuses/lifetimes.

`doctor` checks local directory/socket/database permissions, socket connectivity,
and API/state compatibility through the daemon. It never opens the database beside
the daemon. Failed local checks return nonzero with a typed diagnostic result.
Docker/Caddy checks are explicitly `deferred`; their absence is not a failure and
neither is contacted. A daemon that rejects state at startup reports the actionable
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
and restart. There is no configurable retention policy, artifact collection or
keep-on-failure in this milestone.

Selected containers, publication, non-port resources and fresh-only jobs are
rejected before application launch. Metadata `prepare` retains its allocation-free
semantics for all supported manifest declarations. TCP/HTTP probes additionally
require `lsof` on the daemon PATH, to prove the listener belongs to the supervised
group at the actual dialed destination; an unrelated listener never satisfies
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
interrupted and stopped instances require explicit `restart`, which revalidates
checkout identity, applies compatible command/environment/graph/probe drift, and
retains logical identity. Resource contract changes are rejected before stopping
old work; reset/destroy remain later milestones. Instance mutations and declared
checkout-output groups serialize across scenes/runs. Ctrl-C/SIGTERM during a CLI
run cancels and joins the supervised work. Ordinary persistent client disconnect
does not cancel accepted startup; disposable client loss expires its lease.

An outside guardian owns a separate anchor child in a distinct process group.
The guardian starts and waits for the application root in that anchor group,
reports its original result, and retains the separate anchor until group cleanup. The guardian signals only that pinned group and
does not reap the anchor until no other member remains and the anchor has exited.
No observed member PID is signaled. After anchor join, a kernel signal-0 group
query must prove ESRCH; enumeration alone cannot acknowledge absence. Successful
stop also requires joined log/status
collectors, a private authenticated cleanup receipt, and verified guardian exit.
Control sockets and receipts live in random mode-0700 `/tmp/blctl-*` directories;
their paths and random capabilities stay in the private runtime journal. Missing
root PIDs, EOF and closed listeners do not establish cleanup. Graceful daemon
shutdown stops owned work. Recovery requests cleanup through the same authority
and marks interrupted with an explicit collection gap; it never resumes work.
Missing or mismatched control authority is a cleanup failure that preserves
uncertain survivors. Inspect the private ownership journal before manual action.

Additional typed POST endpoints (same strict v1 negotiation and JSON rules):

| Endpoint | Named request | Named response |
| --- | --- | --- |
| `/v1/run` | `RunRequest` | `InstanceResponse` (accepted startup) |
| `/v1/restart` | `RunRequest` with `instance_id` | `InstanceResponse` |
| `/v1/runtime/stop` | `InstanceRequest` | `InstanceResponse` after verified cleanup |
| `/v1/logs` | `LogsRequest` | `LogsResponse` (bounded page and next offset) |

The CLI waits for persistent ready or disposable terminal status through inspect;
raw API callers must renew disposable capabilities while polling. Logs pages use
an absolute unfiltered record offset, so component filtering preserves cursor
progress. Inspect and cancel remain shared across metadata and native execution.
