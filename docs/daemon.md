# Local daemon and execution

The local daemon owns execution, resource lifecycle and durable metadata.
`prepared` means a validated snapshot was recorded. It never means an application
was executed, a resource was reserved, or readiness was observed. Native execution
and retained logs are documented below. See [containers/private state](containers.md)
for Docker execution and retained resource lifecycle; Publication and retained evidence are documented in the
[HTTPS](https.md) and [evidence](evidence.md) guides.

## Identity and lifecycle

| Term | Meaning |
| --- | --- |
| Project | Stable manifest `project` identifier and project-directory location; a label alone is not ownership |
| Checkout | Canonical filesystem/Git-directory identity; linked worktrees are distinct runtime scopes; branch/HEAD are provenance |
| Scene | Named manifest selection of components/resources with explicit persistent or disposable lifetime |
| Instance | Recorded runtime/metadata identity; persistent project + checkout + scene converges on one instance, disposable invocations get new IDs |
| Attempt | One accepted execution within an instance; separates logs, results, attachment and build-image provenance across starts |
| Retained generation | Owned data/resources and paired generated credentials; can survive multiple attempts until reset/destroy |

A private project-configuration generation is a separate immutable input/tool
file, shared by linked worktrees through registration. It is not the instance's
data generation; resetting daemon state does not erase the registry or old secrets.
See [private configuration](manifest.md#registered-private-project-configuration).

| Intent or edit | Action | What applies or survives |
| --- | --- | --- |
| Discover declarations / resolve current prerequisites | `scene list`, `scene describe` / `plan` | No runtime mutation; plan resolves caller-side tools, inputs and seeds |
| Record metadata only | `prepare SCENE` | Snapshot/identity, no application readiness or provider allocation |
| First persistent start | `scene start SCENE` or `run SCENE` | Accept snapshot/attempt, allocate selected resources, await readiness |
| Join running scene; or resume stopped scene | Repeat start/run | Existing identity, retained snapshot/data/credentials; report drift without applying edits |
| Changed ordinary command, probe or non-sensitive environment | `restart ID` | Capture current sources and apply compatible drift; retain identity/data/paired credentials |
| Changed protected resource/initialization/sensitive contract | `reset ID`, or `destroy ID` then start | Explicit destructive replacement required; reset keeps logical ID but replaces generation; later start after destroy gets a new ID |
| Failed/interrupted persistent attempt | Inspect then `restart ID` | No implicit retry; compatibility/ownership fences apply before old work is stopped |
| Stop development runtime | `scene stop SCENE` or `stop ID` | Stop owned consumers/routes, release verified-unused instance networks, retain data, credentials, history and evidence |
| Remove this instance's owned data | `destroy ID` | Stop/join and remove data, then erase attempt results/logs/artifacts; export first |
| Change daemon PATH/environment or policy | Explicit daemon stop/start in same state directory | Future daemon context changes; retained data/CA remain; no automatic scene restart |
| Inspect or follow | `status`, `logs`, `export` | Observation only; no attachment, lease renewal or lifecycle ownership |
| Own one new persistent attempt until client ends | `scene start --attached SCENE` or `run --attached SCENE` | Refuse existing runtime; signal/loss stops only the accepted attempt and retains data |

Restart rejects protected drift **before stopping old work**: resource declarations,
attachments/mounts, fresh-only contracts, protected container image/args, Docker
endpoint and resolved sensitive/inherited/seed values. The authoritative detailed
rules and build-image exception are in [generations and drift](containers.md#generations-jobs-and-drift).
Reset/destroy are not rollback transactions once destructive cleanup begins.
Cleanup uncertainty is a failure: retain ownership records and retry through the
same scope. Never infer ownership from a name, PID, hostname or empty observation.

Named status/stop use recorded location/identity, even with a broken/deleted
manifest. Use recorded IDs if checkout identity is unavailable or selection is
ambiguous. [Selection rules](#recorded-selection-and-stop) define the exact boundary.
For the daily command sequence see [operations](operations.md#daily-loop); current
flags/defaults and output framing live in the [CLI reference](cli.md).

## Commands

Use an [installed executable](operations.md) (or `make build` for `bin/backlot`):

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
`stopping`; cleanup or collection failure returns nonzero. The acknowledgment is
not proof of final process exit or lock release; shutdown still finishes through
the daemon's existing lifecycle. There is no service installer. Use the same `--state-dir` on each command;
without it, the directory is `backlot/daemon` below Go's `os.UserConfigDir()`.
Actual state/lease/evidence policy comes from daemon startup flags, not machine
`storage` declarations. Inspect it with `daemon config`; see
[effective versus reserved settings](manifest.md#machine-schema-and-results).
Unix socket paths have OS length limits: choose a short private directory if a
long configured path cannot bind.

The directory must be user-owned and private (0700); socket, database, lock and
startup log are private (0600). Unsafe existing permissions, symlinked final state
directories, and symlinked or wrong-type daemon files are rejected, not repaired.
Clients check the permission boundary before dialing. A single-daemon advisory lock
covers startup through database closure. Only a private owned socket with a proven
connection-refused result is removed as stale; live or uncertain listeners remain
untouched. The lock file remains after shutdown and is not a PID ownership claim.

Before `prepare`, `run`, `restart`, `reset`, `scene start`, gateway start/restart or
an explicit mutating Doctor probe, the shared Go client checks current API/state
compatibility with a fresh bounded status request. A running older or
malformed daemon is rejected before scene mutation; no compatibility result is
cached. Historical reads and snapshot cleanup calls do not perform this
configuration preflight. Status and shutdown responses require matching API/state
versions, including `daemon stop`; there is no pre-v9 shutdown exception. Preserve
unsupported state for diagnosis and its matching binary's lifecycle; do not discard
ownership records. Stop the old daemon before upgrading, wait for process exit and
lock release, then explicitly start the new binary. A stopping acknowledgment alone
does not prove either, and adds no polling or automatic restart.

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
or secrets. Do not put credentials in command arguments,
identifiers or paths; use sensitive environment declarations.

## Named persistent scenes

Use `scene` to target a persistent scene by name in the current checkout. Start
validates current inputs and waits for readiness; status and stop use recorded
identity without parsing the current manifest or machine configuration.

```sh
bin/backlot scene start --json dev
bin/backlot scene start --project /absolute/project --config /absolute/machine.yaml dev
bin/backlot scene status --json dev
bin/backlot scene stop --json dev
bin/backlot scene status --follow --interval 1s --duration 30m --json dev
bin/backlot status --follow --json INSTANCE_ID
bin/backlot stop --json INSTANCE_ID
```

Use the daemon's `--state-dir` where needed. Scene commands accept names only;
recorded-ID commands work outside a checkout. Finite `status ID` keeps its existing
`InstanceResponse` output. ID observation includes persistent, disposable and
historical instances; use `run` to execute disposable scenes.

Start accepts `--startup-timeout`, `--job-timeout` and `--stop-grace`. Repeated or
concurrent starts converge on the existing instance and report manifest/config
drift without reconciling retained data. Unhealthy execution needs explicit
restart. A logical project/checkout/scene key recorded at a different canonical
project directory conflicts before start/join. Cancelling startup uses existing
run cleanup; cancelling observation leaves execution alone.

### Attached execution

`scene start --attached dev` and `run --attached dev` start and exclusively own a
new persistent execution attempt. They stay alive at readiness and show aggregate
startup/service logs until the attempt ends. `--json` emits typed NDJSON lifecycle
and log records (`starting`, `ready`, `stopping`, `stopped`, `failure`, `log`, and
explicit gap/warning records); private attachment authority is never command output.
Default starts still return at readiness and leave runtime running. Disposable
scenes, terminal arguments, `--keep-on-failure`, restart and reset reject attached mode.

An attached start refuses existing runtime rather than adopting it. Ordinary
starts may join without gaining authority. SIGINT/SIGTERM stop/join the exact
owned attempt, remove its routes, retain data/paired credentials and exit 130/143
when cleanup succeeds. The daemon uses its existing `--lease-duration` (30s default)
to stop runtime if the client dies or stops renewing, including blocked output.
Stop/restart/reset/destroy remain operator overrides with their original semantics;
old client requests cannot affect a replacement attempt. A verified external stop
exits 0; replacement, daemon change, startup/runtime failure and cleanup/collection
uncertainty are nonzero. No reconnect or automatic restart is provided.

Readiness and runtime liveness retain existing detectors; attachment adds no
continuous health probes. Log delivery uses existing retained pages and gaps,
selecting the accepted attempt without a new ordering/completeness/replay promise.
Standalone status-follow/logs remain observers. See the
[attached execution decision](adr/20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md).

### Recorded selection and stop

The daemon selects the nearest **recorded** project-directory ancestor in the
canonical checkout, using history to discover project locations. If it lacks the
scene, return successful `not_found`; never fall back to an outer project.
`--project` selects an exact directory or recognized manifest path (`backlot.yaml`,
`backlot.yml`, `backlot.json`). A broken/deleted manifest or changed project ID does
not hide recorded instances while the directory and checkout identity remain usable.

Multiple non-destroyed persistent records at that location/scene produce
`ambiguous_target`; use `backlot status ID` or `backlot stop ID`. Failed and
metadata-only records participate; destroyed identity tombstones are available by ID only.
Malformed recorded selectors or disagreement with existing persistent slots produce
`state_corrupt` before discovery filtering. Preserve state for diagnosis; existing
ID inspection/cleanup remains independent of named selection.

Moved/replaced checkouts or unavailable project/Git identity fail selection; use
recorded IDs. No force or path-only adoption exists. Project subdirectories within
an unchanged Git checkout have no separately persisted inode identity; selection
uses their recorded canonical location. Non-Git projects use the recorded project
root as checkout identity; before any record exists, supply `--project` explicitly.
See the [selection rationale and stored-state boundaries](adr/20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md).

Finite named results are `SceneResponse`: API/daemon generation, outcome, optional
`result: InstanceResponse`, and canonical checkout/selector metadata. `not_found`
omits `result` but includes checkout identity. Runtime failures are snapshot data
and status exits zero; selection, validation and transport errors are nonzero.
Human status prints recorded runtime failures.

Stop locks and rechecks the selected ID before verified cleanup; selection changes
conflict rather than redirecting cleanup. Successful outcomes are `stopped`,
`already_stopped` and `not_found`. Data and credentials remain. Metadata-only and
failed records retain truthful status; cleanup/collection uncertainty stays
nonzero. Stop shares mutation serialization with restart/reset/destroy and has no
attempt-specific attachment authority.

### Sampled status follow

Both `scene status NAME --follow` and `status ID --follow` emit initial and changed
snapshots, including instance/attempt, components, origin and failure details.
Named follow watches the original canonical selector through absence and recreation;
ID follow stays pinned across destroy/recreation and never switches to a replacement.
Stopped, failed, destroyed and missing states do not end observation. Intermediate
transitions may be missed; unchanged polling emits no duplicate snapshots.

| Option | Default | Inclusive bounds |
| --- | --- | --- |
| `--interval` | `1s` | `100ms`–`1m` |
| `--duration` | `30m` | `1s`–`24h` |

Both options require `--follow`; zero, negative, malformed or out-of-range values
fail. The total budget includes initial acquisition and in-flight requests. One
cancellable request runs at a time, with the interval after completion.

JSON output is NDJSON `SceneFollowRecord`: `api_version`, observation `time` and
`type`. A `snapshot` carries `SceneResponse`; `complete` carries reason `expired`
or `cancelled`; `error` carries a typed error. Expiry exits zero, independently of
runtime health. SIGINT/SIGTERM cancel observation and exit 130/143. Output errors
are nonzero; a closed pipe cannot guarantee a terminal record.

Both modes pin daemon generation. Named follow also fences the initial checkout
ID/path/Git-directory and canonical selector, including initial absence; branch
and HEAD are provenance only. Daemon loss/replacement, ambiguity, identity loss or
invalid responses terminate nonzero without retry, reconnect or autostart. ID
follow reads recorded state without checkout discovery or manifest access.
Observation never stops execution, renews leases or acquires attachment ownership.

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
[private-state guide](containers.md#state-compatibility) for the v9
format and rejection/cleanup rules.

| Endpoint | Named request | Named response |
| --- | --- | --- |
| `GET /v1/status` | None | `DaemonStatusResponse` |
| `POST /v1/shared/list`, `/v1/shared/inspect`, `/v1/shared/start`, `/v1/shared/stop`, `/v1/shared/restart` | `SharedRequest` | `SharedResponse` (durable status; no credentials/ownership authority) |
| `POST /v1/shared/destroy` | `SharedDestroyRequest` | `SharedResponse` |
| `POST /v1/doctor` | `DoctorRequest` | `DoctorResponse` |
| `POST /v1/instances` | `ListRequest` | `ListResponse` (at most 100 summaries) |
| `POST /v1/stop` | `ControlRequest` | `DaemonStatusResponse` (`stopping`) |
| `POST /v1/prepare` | `PrepareRequest` | `InstanceResponse` |
| `POST /v1/inspect` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/cancel` | `InstanceRequest` | `InstanceResponse` |
| `POST /v1/renew` | `LeaseRequest` | `InstanceResponse` |
| `POST /v1/run`, `/v1/restart`, `/v1/reset` | `RunRequest` | `InstanceResponse` |
| `POST /v1/runtime/stop` | `StopRequest` (force requires selected `expected_attempt`) | `InstanceResponse` |
| `POST /v1/destroy` | `DestroyRequest` (required `contract: "erase-attempt-history/v1"`) | `InstanceResponse` (execution-free destroyed identity) |
| `POST /v1/attempts/list` | `HistoryRequest` | `HistoryResponse` (at most 100 summaries) |
| `POST /v1/attempts/inspect` | `AttemptRequest` | `AttemptResponse` |
| `POST /v1/attempts/prune` | `HistoryPruneRequest` | `HistoryPruneResponse` |
| `POST /v1/export` | `ExportRequest` (optional exact attempt) | `ExportResponse` |
| `POST /v1/attached/renew`, `/v1/attached/cancel` | `AttachedRequest` | `SceneResponse` |
| `POST /v1/logs` | `LogsRequest` | `LogsResponse` |

Restart/reset require a recorded `instance_id`; run does not accept one.
Destroy force uses command-specific destroy requests; native stop force retains its
independent attempt-bound `StopRequest` contract.

`list` uses `ListRequest` (`after_id`, `limit`; zero means default 100, maximum
100) and returns identity-ordered summaries with a `next_after_id` continuation.
Empty lists contain an empty array. Historical/destroyed targets remain discoverable.
`status` is a CLI alias for `inspect`. Every instance response removes snapshot
ownership tokens while preserving the private durable recovery authority. Stop
the old daemon before upgrading; use its matching binary for an incompatible
daemon, following [state compatibility](#configuration-state-compatibility).
Fixture discovery and explicit sensitive access use `FixturesRequest` through
`POST /v1/fixtures` and `/v1/fixtures/secret`; see [evidence](evidence.md).

Attached starts opt in with `attached: true` on `RunRequest` or
`SceneStartRequest`. Only their initial `InstanceResponse` carries `attachment`
(private `owner` with daemon generation, attempt and token, plus expiry and lease
duration). Renewal/cancellation carry that owner and instance ID; their recorded
snapshots contain no capability. Never persist or emit the capability as logs or
ordinary command results. Attachment does not change durable state format 9.

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
commands and descendants, including readiness commands, may create groups within
their live guardian-owned session. Do not use `setsid`, change privileges or delegate
work to an unrelated supervisor. This cooperative contract is not containment.
The [session supervision ADR](adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md) defines bounded observed-empty cleanup,
its residual-process risk and guardian-loss uncertainty. Stock managed Caddy and
internal helpers keep strict pinned-group cleanup.

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
daemon PATH, to prove the listener belongs to the authenticated application session at the actual
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
budgets after the configured grace. Native cleanup can stop each workload twice
(before and after artifact collection), so the outer owner join budgets twice the
accepted grace plus 50 seconds for activation, verification, collection and resource
cleanup overhead; a slower or failed join reports failure and retains state
ownership until the owner exits. Run, scene and attached CLI cleanup waits outlast
that join by 10 seconds so the daemon's typed result, not a client deadline, reports
slow cleanup. Retained (kept) instances stop separately, without this join. The
private `daemon.log` records the initiating cause of each failed native cleanup or
listener-ownership observation behind the bounded public detail.
Disposable clients renew the finite daemon
lease while waiting; unlimited execution never disables lease cancellation.
Expired runs receive independent cancellation coordinators, so one run's grace
or lifecycle lock cannot delay cancellation of another. Sweeps remain responsive
to later expiries and daemon shutdown; shutdown joins execution and cancellation
owners before releasing their store, or reports failure and retains ownership.

Persistent startup is daemon-owned after acceptance, so closing a client leaves
it running. See the [lifecycle table](#identity-and-lifecycle) for join/resume,
restart/reset/destroy and drift decisions, and the
[private-state contract](containers.md#generations-jobs-and-drift) for protected
initialization, credentials and cleanup fences.
Instance mutations and declared checkout-output groups serialize across
scenes/runs. Ctrl-C/SIGTERM during a CLI
run cancels and joins the supervised work. Ordinary persistent client disconnect
does not cancel accepted startup; disposable client loss expires its lease.

A live authenticated guardian leads the application session and owns an unreaped
anchor child in the original workload group. It records the original root exit
separately, acquires kernel process identities, observes session membership and
revalidates identity before individual signals. TERM/grace/KILL scans have finite
candidate, acquisition, handle and verification budgets. Known live identities
remain tracked until exited or zombie; they cannot disappear merely because a
later enumeration misses them. Two successful complete empty observations at
least 20ms apart provide `observed_empty`, never universal descendant absence.
The retained anchor is released only after scans; its original group retains a
kernel absence check. Logs/status/root/guardian joins and an authenticated bound
cleanup receipt remain required. Strict system helpers keep their stronger original
group policy and omit application session confidence results.

`execution.native_cleanup` records a named result per component/attempt:
`confidence` is `observed_empty`, `incomplete` or `authority_lost`, with a stable
`reason` and bounded safe `detail`. Human status, JSON, doctor and portable exports
expose the same observation. `observed_empty` is success under the accepted risk:
an unseen same-session fork or unsupported escape can still race restart or data
cleanup, and ordinary lifecycle/output/data fences may be released. Known live
members, denied observations, exhausted budgets, missing joins or authority loss
remain nonzero and retain fences. An optional `resource_release: previous_boot` records that all relevant launch
obligations, including pending intent, proved an older boot. It preserves historical
confidence/reason/detail and collection gaps. Exact-attempt force warnings can also
explain released native claims; warnings from older attempts cannot release a new
failure. These dispositions never synthesize `observed_empty` or bypass non-native
or abandoned-data deletion guards. Doctor keeps an incomplete historical-observation
check failed and explains the recorded resource release independently from launch eligibility. Root exit, EOF or closed listeners alone never
establish cleanup. No raw process/session identities are public results.

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

Capabilities, protocol/purpose and boot identity remain private. See [state compatibility](containers.md#state-compatibility).

See [disposable results and evidence](evidence.md) for exact exit codes, kept
failures, artifacts, fixture access and retention settings.

Finite startup failures after acceptance retain the last validated instance/attempt
with an optional presentation error, explicitly marked last observed. This does
not establish current state or completed cleanup; see [CLI framing and recovery](cli.md#cli-invocation-and-results).

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

Tool checks show current daemon command/readiness candidates, resolved paths and
winning manifest/machine/private declaration sources from the same lookup.
`current_tool_*` paths/checks describe this observation; `recorded_tool_*` paths
retain original instance selections even when the file is now missing. Human
instance/status output also shows recorded paths. Original successful candidate,
source and lookup context are unrecorded; current checks cannot reconstruct them.
A prepared path does not prove execution, and an absolute npm executable does not
identify the child node selected through its own environment.

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
a normalized certificate-only copy of the matching pinned managed public root,
read-only at `/backlot-public-ca.crt`; it never mounts keys/directories or changes
image/system trust. The copy is mode 0444 under the daemon's existing owned 0700
state directory, so a file-only bind is readable by capless container root.
Original CA/key bytes and private modes remain unchanged. This ephemeral
Doctor-only copy does not change application `ca_cert_path` or trust delivery. TCP/TLS failures remain distinct
from DNS failures; none establishes other application images' trust. Curl clients
can have different `.localhost` resolution behavior and are not this probe contract.

Execution is bounded to ten seconds (within the overall fifteen-second observation
budget); independent cleanup has twenty seconds. The root filesystem is read-only,
capabilities are dropped, privileges cannot escalate, memory/process counts are
bounded, and stdout/stderr are discarded. No command/provider output is exposed.
The probe retains container user `0:0`; readability comes from the public-only
copy, not DAC capabilities or changing original host permissions. No application mounts, secrets or
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

The existing diagnostic journal records copy intent before allocation, then the
owned parent/file identity and planned-byte hash before use. Recovery proves the
container and bridge absent before deleting the authenticated copy and clearing
its journal. Partial allocations with a saved file identity can be removed; a
crash between exclusive creation and saving that identity leaves an unidentified
copy that recovery deliberately preserves. Changed identity, ownership, links,
mode or ready bytes also preserve the copy and journal. Well-formed unresolved
cleanup does not block daemon startup, new probes or application/native fences,
but Doctor remains unhealthy and shutdown reports the unresolved cleanup. Invalid
journal metadata fails startup closed under the existing state-corrupt contract.

Doctor's existing `diagnostic_cleanup` check reports up to eight artifact/cause
entries plus an omitted-entry count (under 2 KiB total). Live cleanup uses the same
sanitized format in `container_probe_cleanup`. A public certificate basename is
derived only from a validated journal key; this correlation identifier is not a
resource token, capability or cleanup authority. Fixed causes distinguish
`unidentified allocation`, `copy mismatch`, `parent mismatch`,
`provider uncertainty` and `removal failure`. They describe the failed cleanup
step, not permission to delete. Provider failures prevent copy inspection, so
restore provider access before interpreting any later copy obstruction. Entries
are a bounded snapshot of failed cleanup attempts; restart retries all pending
journals and can expose entries previously omitted. No provider error, saved
path, key bytes or private journal is included.

For a reported public-copy obstruction, stop the daemon and join any diagnostic
owners first, even when shutdown returns unresolved cleanup. Inspect the exact
reported `doctor-public-ca-<journal-key>.crt` artifact under
the selected private state directory against its private diagnostic journal; do
not infer authority from its filename. After independently establishing that no
relevant Engine consumer remains, an operator may quarantine that exact artifact
outside the daemon state directory for inspection. Preserve its bytes/metadata
and the journal; do not edit the database, blindly unlink files, adopt a changed
file or force-remove foreign resources. Restart with the recorded provider access:
recovery authenticates consumer absence again, and an absent copy can then clear
that journal. If the recorded original parent cannot be established (including a parent
mismatch), consumer ownership or artifact identity cannot be established,
retain the uncertainty and obtain operator help. No automatic foreign-file
cleanup or operator management subsystem is provided. Supported legacy journals without
copy metadata remain supported. Version9 fences older readers; downgrade is unsupported.

## Configuration state compatibility

Registered private project composition uses daemon state version 9, the minimum
supported format. Pre-v9 databases are rejected unchanged. From v9 onward, retain
and apply every required real migration in one transaction for skip-release
upgrades; see the [migration policy](adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).
Status and shutdown responses require matching API/state versions. Stop an
incompatible daemon using its matching binary before upgrading; a stopping
acknowledgment does not prove process exit or lock release or trigger automatic startup.
Existing snapshots remain authoritative for recorded cleanup; missing historical
private/effective provenance stays unrecorded. Eligible historical prepared/stopped starts capture
current project-only values with existing retained-resource/sensitive-drift fences
and adopt them atomically with execution acceptance. Ordinary version 9 stop/start
retains its immutable snapshot. The independent project registry remains format 1.
See [state upgrade and operation](containers.md#state-compatibility) and
[private setup, provenance and retention](manifest.md#registered-private-project-configuration).

## Declared startup details

Persistent startup can show declared fixture details, including selected secrets.
See [selection, output and lifecycle behavior](manifest.md#components-and-scenes)
and the [seeded example](../examples/macos-quickstart/README.md#seeded-demo-account).

## Build-image restart opt-in

`restart INSTANCE_ID --allow-image-change` deliberately accepts changed produced
image IDs for containers with retained mounts, with an application/data compatibility
warning in human output and typed JSON/status/export. Without it, a changed ID
fails after the producer, leaving runtime stopped and old pins/data retained.
Same-ID restart needs no flag. The flag is restart-only, invocation-only, and
cannot bypass other safety checks or affect literal/input-image declarations.
See [image retention](containers.md#execution-scoped-image-retention) and the
[project example](../examples/build-images/README.md). Plans remain symbolic.

## Bounded recorded discovery and active policy

```sh
backlot list --project . --scene dev --lifetime persistent --state stopped --limit 10 --json
backlot list --checkout /absolute/worktree --project-id my-project --json
backlot daemon config --state-dir /private/backlot --json
```

Unfiltered list remains global recorded identity discovery, including destroyed
tombstones; successful destroy retains no execution result or attempt evidence.
`--project` and `--checkout` are mutually exclusive explicit location selectors;
project selects the exact recorded directory (a deleted recognized manifest path
also works), while checkout selects all projects in that existing directory's
verified checkout identity. Neither reads current manifest contents. Project ID,
scene, lifetime and exact instance state intersect the location selection; state
is `Instance.Status`, not historical execution outcome. Explicit IDs remain the
inspection/cleanup fallback for unavailable, moved or replaced identity. Named
scene lifecycle selection retains its nearest-project/ambiguity rules.

Filters apply before the 1–100 page limit. Resume using `--after NEXT_AFTER_ID`
with identical filters; the cursor is the last returned match only when another
match exists. Empty results contain `instances: []`; corrupt records fail.
Pages are independent live reads in lexical ID order: earlier insertions and
changed membership can be missed. They are neither snapshots nor mutation authority.

`daemon config` reports actual state/socket, API/state versions, generation,
lease and evidence retention from captured **daemon-startup policy**. It does not
reload files or invent original flag provenance. `daemon start` against a running
daemon leaves that policy unchanged; inspect it, then explicitly stop/start to
apply different settings. See [local composition](manifest.md#discover-declarations-before-setup)
for prospective caller settings. New read contracts on an older daemon fail with
`unsupported_feature`; no autostart, retry or fallback mutation occurs.
