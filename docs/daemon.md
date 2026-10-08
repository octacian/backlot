# Local metadata daemon

Milestone 2 adds explicit local daemon control and durable metadata preparation.
`prepared` means a validated snapshot was recorded. It never means an application
was executed, a resource was reserved, or readiness was observed. Native execution,
Docker, Caddy, logs and application lifecycle commands remain later milestones.

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
stop request. `daemon stop` acknowledges `stopping`; socket removal marks completed
shutdown. There is no service installer. Use the same `--state-dir` on each command;
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
interrupted records. This milestone has no restart/reset/destroy/adoption operation.
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
`interrupted`. No external resources exist to reconcile in this milestone.

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
