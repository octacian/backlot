# CLI reference

This reference describes the current executable. Use `backlot COMMAND --help`
for its complete options. Contributor build/test tooling lives in
[Testing and tooling](testing.md#commands).

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
| `plan SCENE [--project PATH] [--config PATH] [-- ARGS...]` | Offline selected input/seed/tool resolution; no daemon/provider effects |
| `prepare SCENE [--project PATH] [--config PATH]` | Daemon records metadata only; returns disposable lease when applicable |
| `run SCENE [--project PATH] [--config PATH] [-- ARGS...]` | Execute; persistent returns at readiness, disposable after collection/cleanup |
| `scene start SCENE [--project PATH] [--config PATH]` | Execute/join a named persistent scene in the selected checkout |
| `restart ID [--config PATH]` | Persistent only; read current recorded project/config sources and apply compatible drift, retaining data |
| `reset ID [--config PATH]` | Persistent only; stop, remove old owned generation, then start fresh; destructive |
| `stop ID` / `scene stop SCENE [--project PATH]` | Stop selected runtime/routes, release verified-unused instance networks and retain data; named stop uses recorded selection |
| `destroy ID` | Remove owned runtime/data, then erase attempt results/logs/artifacts; export first |
| `history ID [--limit 100] [--after CURSOR]` | Newest-first retained attempt summaries |
| `inspect ID --attempt ATTEMPT` / `inspect ID --last-completed` | Exact retained result/provenance; mutually exclusive selectors |
| `history prune ID [--older-than D] [--keep N] [--dry-run]` | Union selection; requires age and/or count; protects runtime/cleanup obligations |
| `inspect ID` / `status ID` | Current redacted instance; destroyed identities omit execution; no manifest needed |
| `scene status SCENE [--project PATH]` | Recorded persistent selection; successful `not_found` has no result |
| `status ID --follow` / `scene status SCENE --follow` | Sample without owning runtime; `--interval 1s` (100ms–1m), `--duration 30m` (1s–24h) |
| `list` | One page of recorded history, including destroyed records; `--limit 100` (1–100), `--after NEXT_AFTER_ID` |
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
| `version` / `licenses` | Executable/API/state identity / embedded license and notices; no daemon |

Stop retries network cleanup even for stopped records; named stop may report
`already_stopped` after releasing networks. Consumer, provider or ownership failures
require correction and retry; see [network cleanup](containers.md#ownership-and-recovery).

`list` filters intersect: `--project PATH` (exact recorded project directory) or
`--checkout PATH` (verified checkout), plus `--project-id`, `--scene`,
`--lifetime persistent|disposable`, and `--state` (exact instance status).
These location selectors do not parse current manifest contents; neither pages
nor filters authorize cleanup. See [recorded discovery](daemon.md#bounded-recorded-discovery-and-active-policy).

`project setup` accepts `--from PATH` (`-` for stdin), `--edit`, `--reassociate`,
repeatable `--generate NAME`, `--remove-input NAME`, `--remove-tool NAME`, and
inspection-only `--dangerously-reveal-secrets`. Supplied/generated/removal names
must not conflict; edits preserve unspecified entries and publish only a complete
valid candidate. Values never enter argv. See [private setup](manifest.md#registered-private-project-configuration).

`run`, `restart`, `reset` and `scene start` accept `--startup-timeout 5m`,
`--job-timeout 30m`, `--stop-grace 10s`. Execution budgets accept `0s` for unlimited;
other applicable finite budgets and leases still apply. Stop grace is finite and
nonnegative. `run --keep-on-failure` is disposable only; success still cleans up.
`run --attached` and `scene start --attached` own a new persistent attempt until
client cancellation/loss; no tails or keep-on-failure. `restart --allow-image-change`
explicitly accepts changed build-image compatibility risk. See
[lifecycle/drift](daemon.md#identity-and-lifecycle) before restart/reset.

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
[native recovery](daemon.md#native-execution) and [shared force cleanup](containers.md#explicit-operator-force-cleanup)
before use. Normal cleanup always uses authenticated recorded ownership.

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
validation remains authoritative. See [declaration/configuration discovery](manifest.md#discover-declarations-before-setup),
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
| `cleanup_failure`, `native_cleanup` | Cleanup error and native observation confidence |
| `cancelled` | Cancellation was requested/recorded, independently of original exit |
| Export `published`, `complete`; report `omissions` | Publication and evidence availability, independently of test success |

These execution fields live under `instance.execution` (or
`result.instance.execution` for named results). A zero test exit with failed
collection or cleanup remains a nonzero run. A failed test can have a complete
successful export. `native_cleanup.confidence: observed_empty` is bounded evidence
that permits release under the documented missed-process risk, not universal
absence; see [native supervision](daemon.md#native-execution).

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
