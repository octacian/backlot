<!-- Generated from docs/daemon.md; run make skills-generate. -->

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
| Stop development runtime | `scene stop SCENE` or `stop ID` | Stop owned consumers/routes, retain data, credentials, history and evidence |
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
ambiguous. [Selection rules](daemon.md#recorded-selection-and-stop) define the exact boundary.
For the daily command sequence see [operations](operations.md#daily-loop); current
flags/defaults and output framing live in the [CLI reference](cli.md).

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
[attached execution decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md).

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
See the [selection rationale and stored-state boundaries](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md).

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

## Declared startup details

Persistent startup can show declared fixture details, including selected secrets.
See [selection, output and lifecycle behavior](manifest.md#components-and-scenes)
and the [seeded example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/macos-quickstart/README.md#seeded-demo-account).

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
apply different settings. See [local composition](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/manifest.md#discover-declarations-before-setup)
for prospective caller settings. New read contracts on an older daemon fail with
`unsupported_feature`; no autostart, retry or fallback mutation occurs.
