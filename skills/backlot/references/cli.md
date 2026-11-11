<!-- Generated from docs/cli.md; run make skills-generate. -->

# CLI reference

This reference describes the current executable. Use `backlot COMMAND --help`
for its complete options. Contributor build/test tooling lives in
[Testing and tooling](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/testing.md#commands).

Manifest argv entries may be scalar strings or [explicit composed values](manifest.md#compose-explicit-values),
with one resolved value per argument. Arguments forwarded after `--` stay literal;
Backlot performs no shell expansion and rejects sensitive manifest argv before effects.

## Commands and defaults

In the tables, `SCENE`, `ID`, `SERVICE_ID`, `NAME` and `PATH` are placeholders.
Options belong to the implementing action; `--json` is global. Examples place
Backlot options before `--`, which protects terminal-job arguments. Commands do
not autostart a missing daemon. Unless stated otherwise, daemon-backed actions
accept `--state-dir PATH` and operate only in that daemon's scope.

| Command | Target, defaults and effects |
| --- | --- |
| `scene list [--project PATH]` | Offline sorted declared scenes; no setup/tool/provider prerequisites |
| `scene describe SCENE [--project PATH]` | Offline graph and named prerequisites, without values or readiness claims |
| `config show [--project PATH] [--config PATH] [--state-dir PATH]` | Validated safe caller layers/candidates and prospective defaults; no executable/provider probes |
| `project setup [--project PATH] [--config PATH]` | First terminal setup enrolls private inputs/tools; existing setup inspects. See edit options below |
| `project diagnose [--project PATH] [--config PATH]` | Read-only recorded/current identity, retained owners and blockers |
| `project reassociate --project PATH --checkout-id CHECKOUT_ID [--project-id PROJECT_ID] [--config PATH]` | Read-only exact-owner repair preview; explicit `--apply --preview DIGEST` commits daemon associations |
| `plan SCENE [--project PATH] [--config PATH] [-- ARGS...]` | Offline selected input/seed/tool resolution; no daemon/provider effects |
| `prepare SCENE [--project PATH] [--config PATH]` | Daemon records metadata only; returns disposable lease when applicable |
| `run SCENE [--project PATH] [--config PATH] [-- ARGS...]` | Execute; persistent returns at readiness, disposable after collection/cleanup |
| `task run TASK --scene SCENE [--project PATH] [-- ARGS...]` / `task run TASK --instance ID [-- ARGS...]` | Explicit finite native task against an already-ready persistent parent; returns its independent leased execution result |
| `scene start SCENE [--project PATH] [--config PATH]` | Ensure a named persistent scene is running in the selected checkout |
| `restart ID [--config PATH]` / `scene restart SCENE [--project PATH] [--config PATH]` | Persistent only; read current recorded project/config sources and apply compatible drift, retaining data |
| `reset ID [--config PATH]` / `scene reset SCENE [--project PATH] [--config PATH]` | Persistent only; stop, remove old owned generation, then start fresh; destructive |
| `stop ID` / `scene stop SCENE [--project PATH]` | Stop selected runtime/routes, release verified-unused instance networks and retain data; named stop uses recorded selection |
| `activity [--limit 100] [--after CURSOR]` | One bounded scope-wide activity page, exact infrastructure blockers and recorded/observed provenance |
| `stop --all` | Temporarily fence workload admission, select at most 25 targets completely, stop/join without deleting data or stopping infrastructure; no ID or force |
| `destroy ID` / `scene destroy SCENE [--project PATH]` | Remove owned runtime/data, then erase attempt results/logs/artifacts; export first |
| `history ID [--limit 100] [--after CURSOR]` | Newest-first retained attempt summaries |
| `inspect ID --attempt ATTEMPT` / `inspect ID --last-completed` | Exact retained result/provenance; mutually exclusive selectors |
| `history prune ID [--older-than D] [--keep N] [--dry-run]` | Union selection; requires age and/or count; protects runtime/cleanup obligations |
| `inspect ID` / `status ID` | Current redacted instance; destroyed identities omit execution; no manifest needed |
| `scene status SCENE [--project PATH]` | Recorded persistent selection; successful `not_found` has no result |
| `status ID --follow` / `scene status SCENE --follow` | Sample without owning runtime; `--interval 1s` (100ms–1m), `--duration 30m` (1s–24h) |
| `checkout list [--checkout PATH]` | One checkout, default enclosing Git checkout; same recorded filters/pagination as list, without --project |
| `list` | Daemon-wide by default; one page of recorded history, including destroyed records; `--limit 100` (1–100), `--after NEXT_AFTER_ID` |
| `logs ID [--component NAME] [--follow]` | Retained log chunks; follow observes until runtime settles/interruption |
| `logs ID --page` | Finite page; `--component`, `--attempt`, `--stream stdout\|stderr`, `--after`, `--limit 100` (1–1000) |
| `fixtures ID [--component NAME]` | Discover fixture names with sensitive values redacted |
| `secret ID NAME --component NAME` | Explicitly reveal one sensitive fixture; protect output |
| `export ID --output PATH [--attempt ATTEMPT] [--wait[=SECONDS_OR_DURATION]] [--allow-partial]` | Publish current or exact retained settled attempt to a new caller-owned destination; observer only |
| `doctor` | Daemon access/paths only; selected scopes and opt-in probe described below |
| `renew ID --lease-token TOKEN` | Explicitly renew disposable lease; capability from initial preparation, never inspection |
| `cancel ID` | Cancel preparation/execution using recorded identity; no new work |
| `daemon start` / `daemon serve` | Background handshake / foreground service; capture startup environment and policy |
| `daemon status` / `daemon config` | Connectivity/compatibility / active policy; neither reloads configuration |
| `daemon stop` | Shutdown and owned cleanup; stopping acknowledgment does not prove exit/lock release |
| `gateway start [--config PATH]` / `gateway restart [--config PATH]` | Managed Caddy only; first selection loads explicit/default file, later omission uses retained settings |
| `gateway status` / `gateway stop` | Observe owned process/listeners / stop without removing CA or retained configuration |
| `shared list` / `shared inspect SERVICE_ID` | Recorded shared server identities, allocations and state |
| `shared start SERVICE_ID` / `shared restart SERVICE_ID` | Start/restart one recorded server retaining data |
| `shared stop SERVICE_ID` / `shared destroy SERVICE_ID` | Stop/retain storage or remove allocation-free server/data; consumer fences still apply |
| `contracts [--daemon]` | Focused installed/daemon read capabilities, not a complete flag catalog; state-dir requires daemon mode |
| `schema manifest\|machine\|private` | Raw installed schema, without runtime effects |
| `install-replace --stage PATH --destination PATH --version TAG [--state-dir PATH]` | Foreground installer helper; terminal consent and lock-held atomic replacement; see below |
| `version` / `licenses` | Executable/API/state identity / embedded license and notices; no daemon |

Stop retries network cleanup even for stopped records; named stop may report
`already_stopped` after releasing networks. Consumer, provider or ownership failures
require correction and retry; see [network cleanup](containers.md#ownership-and-recovery).

### Installer replacement

`install-replace` is the text-only foreground adapter for the verified
[release installer](operations.md#release-installer-and-updates), not a general
command runner. It requires distinct absolute stage/destination executable paths,
a private stage directory directly beneath the safe destination parent, and a
matching stable release identity. It opens no database. The default or explicit
selected state root may be created with its standard private lock if missing.
It runs only the recognized old executable's compatible `activity`, `stop --all`
and daemon status/stop commands, requires tty `y`/`n`/`l` consent for a running daemon, and holds state ownership
through fixed rename. Success leaves the daemon stopped; cancellation, unknown
cleanup or pre-rename failure preserves the old executable. Inspection is bounded
by one minute and 100 activity pages; changing membership never proves absence.
Bulk stop has a 135-second client budget for its two-minute coordination; other
old commands have 45 seconds, and shutdown lock waiting has 30 seconds.
Interrupt cancels subprocesses and joins them. Human consent waits
until answered, EOF or interruption. `--json` is rejected with `invalid_arguments`;
there is no successful JSON result. Shell installation options/policy and
capability requirements are maintained in the installation guide.

### Scope-wide activity and workload stop

Use the same `--state-dir` for every command; omission selects the default daemon.
This scope includes recorded projects and worktrees, not every process or state
root on the computer. Neither command reads current manifests, enrolls markers,
repairs registration nor recomposes retained sources.

```sh
backlot activity --state-dir /private/backlot --json
backlot activity --state-dir /private/backlot --limit 10 --after NEXT_AFTER --json
backlot stop --state-dir /private/backlot --all --json
```

`activity` returns `ActivityResponse`: workload/shared entries, recorded managed
gateway status, current preparing-admission count and admission-closed flag.
Workload summaries include current consumer settlement, kept work, recorded
ports/origin and an owner observation (`running`, `joined`, `absent`) in the
reported daemon generation. A joined finite execution owner can still retain
healthy kept services; `kept` and settlement describe those remaining obligations.
Historical status and current settlement can differ:
`interrupted` does not prevent verified release. Use `inspect ID` for historical
outcome, collection gaps and worst native confidence. Recorded server status,
ports and origins do not establish provider health or live listeners. No provider
probe runs and no private capability, command, hydrated input or credential is
included. Shared entries retain their actual `last_transition`; reads never
refresh it.

Each workload's `blockers` names the exact shared service ID or selected managed
gateway guard. `stop` also means restart is blocked. Shared `destroy` means a
retained allocation must first be explicitly released: an allocation can block
destroy while allowing stop. Parent settlement includes dependent task identities.
These diagnoses use the actual lifecycle predicates, including the exact-attempt
route-only recovery exception. Ordinary output suggests `stop ID`, or explicit
`destroy ID` for an idle retained allocation; destroy removes data and history.
JSON callers can construct those commands from validated recorded IDs. Preserve
this command's state directory when acting on them.

Pages contain at most 100 entries and 512 KiB of encoded entries. Reads charge
skipped records and dependent settlement/release-proof scans against 10,000 scans
and 64 MiB per page. Owner observation also refuses more than 10,000
current lifecycle owners. `next_after` resumes after the last fully evaluated record;
`complete: false` means the observation is incomplete, even with no entries.
An entry that cannot fit or be evaluated within a fresh page fails with
`scope_too_large`; use known recorded IDs for diagnosis. Changing membership and
pagination never prove global absence. A settled workload without a retained
shared allocation is omitted; shared server intent remains visible.

`stop --all` requires explicit all intent, no positional ID, and no `--force`
option (including `--force=false`). It closes admission before selection and
permanently invalidates earlier prepare/run/attached/scene/task/restart/reset
tickets, including requests resolving inputs or waiting for a lifecycle lock.
Reopening cannot revive an old ticket. Already-registered queued execution is
selected too. Reads, renewal, ordinary cleanup and explicit infrastructure
controls remain usable. Preceding admissions have a 20-second join budget within
the two-minute coordination budget; an unjoined owner remains unresolved.

Before acceptance, bulk stop refuses more than 10,000 current lifecycle owners.
The complete selection must fit 25 targets, 10,000 charged scans and 64 MiB before
any target stop is issued. Selection corruption/overflow refuses target stops;
prior admission invalidation may already have effects. The target limit reserves
space for worst-case settlement diagnostics under the existing 1 MiB wire limit.
`selection_complete` and `selection_failure` distinguish refusal from an empty
successful selection. Busy targets are unresolved without a new cleanup attempt.
Dependent tasks join before parents; uncertain ownership is never a force bypass.

The finite `StopAllResponse` contains a transient `operation_id`, daemon generation,
complete selected target identities/attempts/generations, and each `completed`,
`failed` or `unresolved` outcome. `settlement_observation` labels selection-time
versus after-stop evidence. Collection failure is separate from surviving
consumers: a failed target can be settled. Partial failure returns nonzero with
this usable response and its `partial_failure` error, rather than replacing it
with an error-only envelope. Uncertain targets and unjoined admissions preclude
all-success. The temporary fence reopens when coordination settles unless daemon
shutdown has permanently closed admission. Completion describes this finite
selection; it does not promise that the daemon stays idle.

Bulk cancellation suppresses investigation keep and retains persistent **and
bulk-cancelled disposable** data, allocations, paired credentials, history and
evidence. Only authenticated runtime resources are released after joining their
consumers. Idle retained data, shared servers, gateway and daemon are left alone.
Previously accepted destructive cleanup and application writes cannot be undone.
Ordinary disposable completion/cancellation keeps its existing cleanup policy.

SIGINT/SIGTERM, timeout or disconnect ends waiting, not accepted cleanup. Existing
execution owners can continue after bounded joining. Started kept/recovery cleanup
retains its registered shutdown-joined owner and exact-attempt serialization until
it finishes, including after the fence reopens. There is no operation journal,
replay, cancellation endpoint or automatic retry. After a missing reply, inspect
activity and known targets in the same scope before explicit recovery; operation
IDs cannot be replayed after a daemon restart.

### Named lifecycle mutations

`scene restart`, `scene reset` and `scene destroy` select exactly one recorded
persistent scene in the nearest recorded project, or the exact `--project` location.
Missing, disposable-only and ambiguous selections fail nonzero; candidate ID
samples are bounded, with `list --scene SCENE` guidance for further discovery.
Use recorded IDs when checkout paths are unavailable. Names never select a newest
disposable run or fall back to an outer project.

Restart/reset accept the same config, startup/job timeout and stop-grace options
as their ID counterparts; restart alone accepts `--allow-image-change`. Attached
mode remains unsupported. Destroy accepts the existing `--force`, with unchanged
ownership limits. Export first, using `--allow-partial` when needed. Restart
retains data; reset replaces the owned data generation under the same ID; destroy
erases data and retained evidence. Neither destructive command is a rollback.
The daemon revalidates selection under lifecycle serialization before effects;
selection changes conflict. Results use the same `InstanceResponse`, waiting,
cancellation and success-only startup-details disclosure as ID commands. A missing
mutation reply means unknown completion: inspect before explicit recovery and
never automatically retry by dropping the named selector.

### Recorded list filters

`list` filters intersect: `--project PATH` (exact recorded project directory) or
`--checkout PATH` (verified checkout), plus `--project-id`, `--scene`,
`--lifetime persistent|disposable`, and `--state` (exact instance status).
`checkout list` defaults to the enclosing Git checkout of the canonical working
directory, including subdirectories and linked worktrees. Outside Git, supply
`--checkout PATH` or use recorded IDs. Explicit `--checkout PATH` on either list
command names the exact checkout root, including non-Git roots; it does not search
upward. Missing/replaced locations fail rather than enrolling or repairing them.
Checkout filters intersect the whole checkout, across recorded projects;
`checkout list` has no `--project` replacement. Root `list` stays daemon-wide by
default and retains exact-project filtering.

Location-scoped human lists print `Checkout: PATH`; JSON includes daemon-resolved
`checkout` metadata alongside `instances`, even for empty pages. This scope does
not establish global absence. Old daemons require an upgrade for scoped output;
there is no fallback that drops the scope contract.

These location selectors do not parse current manifest contents; neither pages
nor filters authorize cleanup. See [recorded discovery](daemon.md#bounded-recorded-discovery-and-active-policy).

`project setup` accepts `--from PATH` (`-` for stdin), `--edit`, `--reassociate`,
repeatable `--generate NAME`, `--remove-input NAME`, `--remove-tool NAME`, and
inspection-only `--dangerously-reveal-secrets`. Supplied/generated/removal names
must not conflict; edits preserve unspecified entries and publish only a complete
valid candidate. Values never enter argv. See [private setup](manifest.md#registered-private-project-configuration).

### Reassociate retained project data

```sh
backlot project diagnose --project /destination --config /private/machine.yaml --json
backlot project reassociate --project /destination --project-id PROJECT_ID --checkout-id CHECKOUT_ID --config /private/machine.yaml --json
backlot project reassociate --project /destination --project-id PROJECT_ID --checkout-id CHECKOUT_ID --config /private/machine.yaml --apply --preview DIGEST --json
```

Diagnosis/preview is read-only. Select exact retained IDs from diagnosis, stop/join
all affected consumers, resolve blockers and acknowledge a fresh preview. The SQL
server may stay live. Apply preserves IDs, data, credentials, allocations and cleanup
authority; it never automatically adopts, merges, renames or cleans up projects.
Relative project location and manifest filename must remain unchanged.

Ordinary enrolled recognition uses the same local marker at the same canonical path;
it needs no stop or repair after a device-number change. Physical source checks still
protect each preparation/launch/task operation. Repair missing/self-pointing Git
`commondir` metadata explicitly. `project diagnose` and typed registration errors
report roles, paths, retained owners, staged publication and the next action.

For a different-path copied marker, choose which exact checkout receives fresh IDs:

```sh
backlot project reassociate --project /copy --fresh-identity-at /copy --json
backlot project reassociate --project /copy --fresh-identity-at /copy --apply --preview DIGEST --json
backlot project reassociate --project /copy --fresh-identity-at /original --checkout-id ORIGINAL_ID --json
```

Without `--json`, an interactive conflict offers the two paths and returns a preview.
Apply still requires its exact digest and `--apply`. Choosing the copy leaves the
original's consumers and private inputs alone. Choosing the original requires its
exact retained checkout ID and stopped/joined affected consumers satisfying the
established cleanup checks; retained owners,
data and credentials stay at the original path. A genuine linked common directory
stays shared; copied common metadata gets a fresh common role. Other projects at a
changed common anchor are included in the affected claims, without recomposing their
scenes. The copy needs explicit new private setup and receives fresh daemon owners.

Marker publication and the selected store are separate stages. A failed/interrupted
stage remains fenced with its exact owner and path; inspect a fresh preview and
explicitly apply, never blindly retry. Private setup is independently staged. After
rotating the original, `project setup --reassociate --project /original` reconciles
its private marker mapping while retaining the same immutable generation. Restore
missing originals or resolve their exact ownership explicitly; no stale claim expires.
Checkout-only projects omit `--project-id`; real retained shared obligations still
require their exact shared owner. No shared owner is fabricated for native/no-SQL work.

For mismatched legacy private setup, run `project setup --reassociate --from FILE` with explicit
values, then repreview; credentials are never imported implicitly. The selected
registry (`BACKLOT_PROJECT_CONFIG_DIR` or user default) must agree with the daemon's.
Align it and restart the daemon if needed; no operation-specific override persists.
Private setup may succeed while daemon repair remains unapplied.

Protected checks remain strict: moving even an unchanged relative fresh-only
initializer such as `./bin/setup` blocks if its resolved executable changes. A
nondefault fresh-only `working_directory` blocks if its effective directory changes;
omission and `.` remain equivalent. Unresolved peers keep their own bindings/launch
facts and cannot start until separately verified; the requesting branch never
substitutes for their scene configuration.

JSON distinguishes recorded/current identity, reports safe blockers and a `preview`
acknowledgement, and confirms apply with `applied`. Inspection separates historical
`checkout`/`plan` from current `association`. Blocked preview is a successful diagnostic;
apply errors `reassociation_blocked`, `stale_preview` and `conflict`, or precommit
cancellation, preserve associations. A lost reply or `reassociation_committed_uncertain`
may mean apply committed: inspect current bindings/accepted acknowledgement, then
repreview; never retry apply automatically. Restart invalidates previews. Recorded-ID
cleanup remains available independently of current sources.

`run`, `restart`, `reset` and `scene start` accept `--startup-timeout 5m`,
`--job-timeout 30m`, `--stop-grace 10s`. Execution budgets accept `0s` for unlimited;
other applicable finite budgets and leases still apply. Stop grace is finite and
nonnegative. `run --keep-on-failure` is disposable only; success still cleans up.
`run --attached` and `scene start --attached` own a new persistent attempt until
client cancellation/loss; no tails or keep-on-failure. `restart --allow-image-change`
explicitly accepts changed build-image compatibility risk. See
[lifecycle/drift](daemon.md#identity-and-lifecycle) before restart/reset.

Persistent `run` and `scene start` wait for starting work or return an already-ready
scene, reporting unapplied configuration changes. After preparation, stop, failure,
cancellation or interruption, they can start one new attempt once prior work and
cleanup finish. Identity, data and paired credentials survive. New attempts use
current compatible configuration from `--config` or the normal per-user default;
they do not reuse the recorded path. Restart/reset keep their existing path defaults.
Use `restart ID` to apply edits to running work; protected changes require reset/destroy.

Overlapping starts share one attempt while it remains current, even if it finishes
before the response. Replacement returns `startup_conflict` with a nonzero exit:
inspect the returned instance/attempt using `history`, `inspect`,
`logs --page --attempt` or `export --attempt` before deciding to start again.
Polling conflicts retain the last observed snapshot without the replacement's
startup details. A missing mutation reply leaves acceptance unknown; never retry
automatically. Startup cancellation stops only its accepted attempt; ID-only
operator stop remains an override. Uncertain cleanup blocks a new attempt;
collection failure alone does not. See [lifecycle](daemon.md#identity-and-lifecycle).

Daemon startup accepts `--lease-duration 30s` (100ms–24h; zero selects the default),
`--retention-attempts 100` (positive completed count), `--retention-age 168h`,
`--retention-bytes 1073741824` (1 GiB); age/byte retention values
cannot be negative; zero selects the corresponding default. The default state directory is `backlot/daemon` beneath
`os.UserConfigDir()`. Starting against an existing daemon leaves its policy intact.
`--config` selects machine configuration, not daemon state/policy or private inputs;
see [configuration and defaults](manifest.md#machine-schema-and-results).

Doctor accepts either `--scene SCENE [--project PATH] [--config PATH]` or
`--instance ID`. Project/config require scene; instance excludes them. Optional
`--container-probe-image ALREADY_PRESENT_IMAGE` authorizes a bounded owned probe;
`--probe-public-ca` additionally mounts only the managed public root. Ordinary
checks are read-only and never pull, repair, install trust or start the application.
See [requested scope and diagnostic limits](daemon.md#scoped-provider-diagnostics)
and the [probe image contract](daemon.md#explicit-owned-container-probe).

`stop --force ID` and `gateway stop --force` can relinquish unresolved native
claims with survivor risk. `destroy --force ID` and `shared destroy --force SERVICE_ID`
waive shared ownership proof only; consumer/native/private-resource fences remain.
These are distinct recovery decisions, not general cleanup bypasses. Read
[native recovery](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/daemon.md#native-execution) and [shared force cleanup](containers.md#explicit-operator-force-cleanup)
before use. Normal cleanup always uses authenticated recorded ownership.

## Declared task invocation

```sh
backlot task run maintenance --scene dev --project . --json -- --dry-run
backlot task run maintenance --instance PARENT_ID --state-dir /private/backlot --json
```

Choose exactly one `--scene` or `--instance`. Named targeting uses the existing
recorded checkout/project selection; `--project` applies only to `--scene`.
Arguments after the task name are literal; use `--` for application flags.
The parent must already be ready with a matching accepted capture. Missing capture
or relevant drift returns `restart_required`; explicitly restart the parent to
accept current declarations. There is no `--config`, implicit start/restart,
keep-on-failure or interactive shell mode. Held output groups or overlapping paths
return `output_conflict` immediately.

`--job-timeout` defaults to 30m and `--stop-grace` to 10s; `0s` job timeout is still
subject to the finite client lease. The client waits, renews its task lease and
on SIGINT/SIGTERM cancels/joins only that task. Successful JSON is one
`InstanceResponse`; `instance.task` gives parent instance, attempt, generation and
task name. Its own `instance.id` addresses `inspect`, `status`, `logs`, `history`,
`renew`, `cancel` and `stop`. The task preserves the exact application exit code;
orchestration, collection and cleanup failures remain separate and return nonzero.
Task cleanup cannot be waived with `stop --force`.
A lost mutation reply means unknown acceptance; inspect recorded scope before
explicit recovery and never automatically retry.

## CLI invocation and results

Use `backlot --help` or `backlot COMMAND --help` for targets, flags and defaults.
`--json` is global; other options belong to their documented action. Unsupported
options and duplicate singleton flags fail. Project setup's `--generate`,
`--remove-input` and `--remove-tool` are repeatable.

`plan` and `run` take the scene first and forward remaining positionals to its
terminal job, with or without `--`. Use `--` to protect application flags. Spaces
and empty arguments stay intact; Backlot performs no shell expansion:

```sh
backlot --json plan test --project . -- -run TestExample
backlot run test --project . -- -run TestExample
backlot run test focus
```

The last command forwards `focus`. Persistent scenes and attached starts reject
terminal tails. `restart` and `reset` take one recorded instance ID and derive its
project; they reject `--project` and terminal tails. `--allow-image-change` is
restart-only. `--keep-on-failure` requires disposable `run`; `--attached` requires
`run` or `scene start` without terminal arguments or `--keep-on-failure`.

Inspection can succeed for a failed execution; doctor returns nonzero for failed
checks while retaining findings. Application exits and collection/cleanup outcomes
remain separate; an absent terminal exit means unknown. Human and JSON output use
the same observed facts. Carry the original `--state-dir` into follow-up commands.
See [results and evidence](evidence.md) for exit and cancellation rules.

| Machine stdout mode | Framing |
| --- | --- |
| Finite operations | One named JSON result, or `ErrorResponse` without a usable result |
| `logs` without `--page`, including finite reads | `LogRecord` NDJSON; zero records is valid; failure adds one `ErrorResponse` line before or after records |
| `logs --page` | One `LogPageResponse`, including continuation and evidence gaps |
| ID or named status with `--follow` | `SceneFollowRecord` NDJSON |
| Attached starts | `AttachedRecord` NDJSON |
| `install-replace` | Terminal/text only; `--json` rejected with `invalid_arguments`, no successful JSON document |
| `daemon serve` | Foreground service; no successful JSON document; normal error framing on failure |
| `schema` | Raw finite JSON Schema in all modes |
| Explicit help, no-action root/group help, `licenses` | Human text even with `--json`; canonical license bytes |

Usage/semantic failures emit `invalid_arguments` without human help on stdout
when JSON is already recognized. Earlier parser failures may use stderr and a
nonzero exit; moving `--json` after an invalid option does not guarantee JSON.
Diagnostics include safe option names and help guidance, omitting rejected values.

Postacceptance failures can return `InstanceResponse` or `SceneResponse` with an
optional `error` and the last validated snapshot. Consumers must allow that field,
check exit status/error, and treat the snapshot as last observed, not current or
final. Without a usable snapshot, output is `ErrorResponse`. Follow/attached errors
also retain known snapshots/results. Retained failures omit lease/attachment
capabilities and hydrated startup secrets; authorized disclosure is unchanged.
Logs keep their stderr warnings and retention-gap notices.

A missing or invalid mutation reply leaves acceptance/completion uncertain. Inspect
the known ID, scene or service with the same state directory before recovery;
Backlot does not automatically retry. Preflight compatibility refusal instead
requires matching-binary recovery. `daemon stop` acknowledges stopping, not process
exit or lock release. Output-write failures are nonzero; a closed pipe cannot
promise a final document or record.

## Installed read contracts and schemas

```sh
backlot contracts --json
backlot contracts --daemon --state-dir /private/backlot --json
backlot schema manifest > manifest.schema.json
backlot schema machine > machine.schema.json
backlot schema private > private.schema.json
```

Local contracts reports existing executable version identity, matching schema
selectors and focused supported reads/output modes. Daemon contracts reports
API/state/generation and its finite read endpoints (excluding local declarations),
without attributing the caller's
build identity to the daemon. Capabilities do not prove provider readiness.
This inventory is not a command/flag catalog. `version --json` remains unchanged.

`schema` emits the exact embedded Draft 2020-12 document in both human and JSON
modes, a finite raw-schema exception to ordinary result envelopes. Runtime strict
validation remains authoritative. See [declaration/configuration discovery](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/manifest.md#discover-declarations-before-setup),
[recorded filters and active policy](daemon.md#bounded-recorded-discovery-and-active-policy)
and [finite log pages](evidence.md#finite-retained-log-pages).

## Interpreting machine results

Capture stdout, stderr and the process exit independently. Parse the command's
named result even on nonzero exit; stdout does not have a universal `result`
envelope. `run`/ID status use `instance`; named start/status use
`result.instance`. Inspecting a failed run can exit zero because inspection
succeeded. Do not treat a zero observer exit as a passing test.

The following are **selected-field illustrations**, not complete wire documents.
IDs are placeholders; omitted unrelated fields are still present in real output.
A successful disposable `run` exits zero with facts like:

```json
{"api_version":"v1","instance":{"id":"ID","status":"succeeded","execution":{"attempt":"ATTEMPT","outcome":"succeeded","terminal_exit_code":0}}}
```

An ordinary test assertion failure can return the original exit (for example 1)
with a usable instance and no transport error:

```json
{"api_version":"v1","instance":{"id":"ID","status":"failed","execution":{"attempt":"ATTEMPT","outcome":"failed","terminal_exit_code":1}}}
```

A later observation failure can instead return nonzero with a **last observed**
snapshot. It neither claims the attempt finished nor authorizes a blind retry:

```json
{"api_version":"v1","error":{"code":"daemon_unavailable","message":"last observed snapshot; inspect the recorded target"},"instance":{"id":"ID","status":"starting","execution":{"attempt":"ATTEMPT"}}}
```

Without a usable snapshot, an error has only `api_version` and `error` (code,
message and optional source/field context). `terminal_exit_code` absent for a disposable terminal job means
**unknown**, never zero; persistent scenes have no terminal job. Optional booleans/failure strings may be omitted when
false/empty; absence of a whole execution or historical provenance is not proof
of completed work. Preserve these independent observations:

| Field | Meaning |
| --- | --- |
| `instance.status` | Current recorded lifecycle; may later become stopped/destroyed |
| `execution.outcome` | Recorded execution outcome preserved across later cleanup |
| `terminal_exit_code`, component `exit_code` | Original observed application exit; absent if unknown |
| `failure`, `failure_detail` | Orchestration/runtime failure and safe detail |
| `collection_failure` | Evidence collection failed, independently of test exit |
| `cleanup_failure`, `native_cleanup` | Current cleanup error and retained native observation confidence |
| `execution.settlement` | Current attempt/generation consumer release, operation cause, observed time and bounded blockers |
| `cancelled` | Cancellation was requested/recorded, independently of original exit |
| Export `published`, `complete`; report `omissions` | Publication and evidence availability, independently of test success |

A settled consumer does not turn an interrupted or failed execution into success.
Stop/status report current release separately from original exits, native confidence
and collection gaps. `settlement.state` is `settled`, `unsettled` or `unknown`;
missing legacy proof remains unknown. Its observation time comes from verification,
never from reading status. An unknown or incomplete stop returns nonzero and
preserves ownership; inspect its blockers before retrying. These diagnostics confer
no cleanup authority on clients. Selected-attempt exports retain the same separation.

These execution fields live under `instance.execution` (or
`result.instance.execution` for named results). A zero test exit with failed
collection or cleanup remains a nonzero run. A failed test can have a complete
successful export. `native_cleanup.confidence: observed_empty` is bounded evidence
that permits release under the documented missed-process risk, not universal
absence; see [native supervision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/daemon.md#native-execution).

For a finite log page, successful access may still return partial/unknown evidence:

```json
{"api_version":"v1","instance_id":"ID","records":[],"continuation":"CURSOR","more":true,"active":false,"historical_collection":"unknown","gaps":[{"code":"collection_incomplete","detail":"known collection gap"}]}
```

An empty page may advance the cursor because filtering consumes scan budget.
Continue using `continuation` and identical instance/component/attempt/stream
filters; `more` is based on observed source sizes. `historical_collection: unknown`
never becomes complete merely because no records/gaps were found. See
[finite log pages](evidence.md#finite-retained-log-pages) for bounds and source changes.
For instance lists, continue with `--after NEXT_AFTER_ID` and identical filters
until `next_after_id` is absent; additions/changed membership can be missed across
live pages. Neither pagination mode is a snapshot or durable subscription.

NDJSON consumers parse each line independently and discriminate record `type`
where defined (follow/attached). Ordinary logs contain `LogRecord` lines with
instance/component/attempt/time/stream and `message` or base64 `data`; chunks need
not be complete application lines. An error line can follow valid records, and
a closed pipe may prevent a final record. Stderr retains diagnostics, ownership
warnings and ordinary-log retention gaps. Preserve both streams and exit status.

Ctrl-C/SIGTERM during run/start requests bounded cancellation and joins cleanup;
clean cancellation returns 130/143, while infrastructure/collection/cleanup failure
can return generic nonzero instead. Disposable client loss expires its lease;
ordinary persistent client loss leaves accepted work daemon-owned. Attached loss
stops its owned attempt through lease expiry. Status-follow cancellation returns
130/143 and stops only observation; expiry returns zero regardless of runtime
health. Log-follow interruption ends observation without stopping runtime.
Export interruption stops waiting/publication, not execution. See
[results and cancellation](evidence.md) and [attachment](daemon.md#attached-execution).

Protect outputs: ordinary inspection redacts secrets, but explicit `secret`/local
reveal and declared successful persistent `startup_details` can disclose them.
Applications can print secrets into logs/artifacts; no arbitrary content filter
is promised. Share reviewed exports, never raw daemon state or private generations.
