<!-- Generated from docs/evidence.md; run make skills-generate. -->

# Disposable results and evidence

`backlot run <scene> -- <arguments>` forwards the final argument vector directly
to the disposable scene's designated terminal job. Other components receive only
their declared arguments. Arguments containing spaces remain single arguments;
Backlot performs no shell expansion. Plain positional tails are also accepted;
use `--` to protect flag-like application arguments. See [CLI invocation and
framing](cli.md#cli-invocation-and-results).

On an otherwise clean run, the executable returns the terminal job's exact exit
code. SIGINT and SIGTERM request bounded cancellation and return 130 and 143.
An orchestration, collection or cleanup failure returns a generic nonzero code;
the original terminal status remains in `execution.terminal_exit_code` and the
component result. Unknown job exit status remains absent; observation or collector
failures never substitute an application exit code. Later verified status recovery
preserves the observed code alongside the separate failure. `failure`, `collection_failure`, `cleanup_failure` and
`cancelled` describe separate outcomes. Application native cleanup also records
`native_cleanup` per component/attempt with `observed_empty`, `incomplete` or
`authority_lost` confidence and stable reason/detail. Observed empty deliberately
permits lifecycle/data fence release despite possible unseen descendants; it is
not universal absence. Known incomplete/lost authority remains a cleanup failure.
Strict system helpers keep stronger group proof and omit session results. Finite JSON commands emit one typed result;
application output is available through `logs`. Backlot captures application
stdout/stderr as emitted, including any secrets the application prints. Backlot's
own diagnostic messages redact resolved sensitive values.

Disposable clients renew a finite lease while waiting. Losing the client stops
renewals; the daemon cancels after the configured lease expires, gathers available
evidence, and cleans up. Execution deadlines of zero do not disable the lease.

## Native runner cancellation

A signal to `backlot run` requests cancellation; it is not forwarded unchanged
to the test runner. Native cleanup uses bounded TERM-based supervision. The
outer CLI's 130/143 and the original native exit (or absence) remain separate.

Native runner cancellation can leave partial evidence without finalized reports.
Neither the [Playwright recipe](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/native-playwright/README.md) nor Go's
normal `TestMain` report writer promises final reports on cancellation. Available
partial files vary by runner/platform and termination; preserve them without
assuming a particular report or media file will exist.

Export `complete: true` means the declared evidence inventory was collected and
audited; application report finalization is a separate fact. Keep available
partial evidence and inspect the original result and collection/cleanup errors.
A runner-directed graceful cancellation capability is tracked separately in
[#84](https://github.com/octacian/backlot/issues/84); the recipe adds no signal
translation.

Attached persistent commands emit typed lifecycle/log NDJSON and aggregate only
the accepted attempt. They retain the same log encoding, gaps and retention limits,
without a replay or completeness guarantee. Their signal/loss cleanup preserves
persistent data and paired credentials. See [attached execution](daemon.md#attached-execution)
for leases, operator overrides and exit results.

## Declared artifacts and fixtures

Components can declare named artifact sources and discoverable fixture identities:

```yaml
components:
  tests:
    kind: job
    runtime: native
    policy: each-start
    command: {tool: test, args: []}
    artifacts:
      report: {path: reports/results.json}
      traces: {path: reports/traces}
    fixtures:
      user:
        description: Test account
        value: {literal: fixture-user}
      password:
        description: Test account credential
        value: {ref: {kind: resource, name: credential, field: value}}
    resources: [credential]
```

Native artifact paths are safe paths relative to the manifest directory inside
the checkout. They may identify declared output locations. Container artifact
paths are absolute paths inside that component's container, such as `/tmp/report`.
Artifacts are collected only for components launched in the execution attempt;
unlaunched or skipped components do not claim existing checkout reports. Files
and directories are copied into private daemon storage before containers
or data are removed. Links and special files are rejected; paths cannot traverse
outside the checkout or extraction directory. A launched component's declared
missing source or failed copy sets collection failure and prevents successful
overall execution. Successful copies appear in `execution.artifacts` with
component, name, attempt and local path.
Partial copies are subject to the same evidence retention policy.

Fixture values use the existing literal/reference contract and component attachment
rules. Values marked `secret`, secret inputs, and generated secret resources stay
redacted in plans, results and ordinary fixture discovery. Resolved named fixture
values are captured for subsequent inspection, including after teardown:

```sh
backlot fixtures --state-dir /private/backlot --json INSTANCE_ID
backlot fixtures --state-dir /private/backlot --component tests INSTANCE_ID
backlot secret --state-dir /private/backlot --component tests --json INSTANCE_ID password
```

`secret` retrieves a named sensitive fixture through `/v1/fixtures/secret`;
`/v1/fixtures` returns ordinary metadata. Persistent startup uses the sensitive
endpoint for [declared selections](manifest.md#components-and-scenes), without
adding their values to execution results, status, diagnostics or exports. The
permissioned local socket is the authorization boundary; protect sensitive output
and private daemon state.

## Investigation and destruction

`backlot run <scene> --keep-on-failure` preserves remaining resources after job,
orchestration or collection failure, including cancellation and client loss.
It stops finite/in-flight jobs and preserves healthy dependency services, routes,
and data. It does not restart failed services. Results set `execution.kept` and
remain failed or cancelled; retained resources never turn a failure into success.

```sh
backlot inspect --state-dir /private/backlot --json INSTANCE_ID
backlot logs --state-dir /private/backlot --component tests INSTANCE_ID
backlot logs --state-dir /private/backlot --follow --json INSTANCE_ID
backlot destroy --state-dir /private/backlot --json INSTANCE_ID
```

Explicit stop or daemon shutdown stops verified kept runtime while retaining data.
Explicit destroy removes owned runtime/data after verified stop, then irreversibly
erases this instance's attempt results, logs, artifacts and private original-result
copies. Stop retains history and evidence. Export needed diagnostics before destroy;
caller-owned exports survive. Failed or uncertain runtime/data cleanup retains
diagnostics, and file-erasure failures return nonzero with retryable deletion intent. Daemon crash recovery stops only verifiably owned
survivors, retains data/evidence and reports interruption/collection gaps. Diagnose
the cause before explicitly starting a persistent scene again. Unknown ownership remains untouched with a cleanup failure unless a targeted
`stop --force` explicitly relinquishes the native claim. That acknowledgement
retains private history, known exits, a collection gap and safe public warnings;
unknown survivors may still run/write. Replacement attempts retain the warning but
do not inherit an old attempt's collection gap. Selected-attempt export can therefore
be complete for a fully collected replacement while reporting prior waivers.

An explicit persistent start after a repaired failure accepts one eligible attempt;
the previous failure, collection gap and original exits stay in retained history.
A later attempt can replace a concurrent start's binding and cause
`startup_conflict`; use exact-attempt diagnostics rather than repeating blindly.
Cleanup uncertainty still blocks replacement. See [ordinary starts](cli.md#commands-and-defaults).

## Logs and retention

Historical and following logs read the same retained records outside native groups
and containers. JSON following emits typed NDJSON records with instance, component,
attempt, timestamp, stream and original output chunk. Non-UTF8 chunks use base64
`data`. Component filters preserve the unfiltered cursor. Human logs include the
same context. Collection and interruption gaps are reported explicitly.

Daemon-wide evidence defaults to 168 hours and 1 GiB, with 100 completed attempt
records per instance. Configure the daemon at
startup (independent of scene machine configuration):

```sh
backlot daemon start --state-dir /private/backlot --retention-age 168h --retention-bytes 1073741824 --retention-attempts 100
```

The daemon periodically prunes oldest completed evidence first when age or total
size exceeds the limits. Active evidence, including kept investigation runtime,
is protected even when it alone exceeds the size cap. Pruning never removes
runtime or retained data. Expired result metadata remains discoverable with
`execution.evidence_expired`; logs report that the evidence expired. A committed
expiration with incomplete file removal is retried on subsequent sweeps/restart.
Count limits remove eligible completed history records and their evidence; age/byte
limits retain metadata with expiration flags. Active, kept and unresolved cleanup
obligations are protected and can exceed limits. Successful destroy erases history
and evidence immediately; only a minimal identity/location tombstone remains.
Private snapshots and named fixtures are current instance state; history never
archives fixtures, private configuration or capabilities.

Completed legacy results without a recorded completion time start their retention
age at the upgraded daemon's first observation. That inferred timestamp
is persisted as `execution.evidence_observed_at` once; daemon restarts do not reset
it, and old file mtimes do not cause immediate age expiration. The size cap still
applies. Active, kept and cleanup-uncertain runtime remains protected.

Pre-history mixed logs/artifacts can contain overwritten attempts with no retained
result. Automatic age/byte expiry still covers those unrepresented bytes using the
instance's persisted completion/legacy observation clock, never file mtime or a
restart-renewed timestamp. Preserve represented protected/unexpired evidence and
all unknown bytes while any retained cleanup obligation makes the instance unsafe.
Existing deletion intents and legacy rewrite fences apply; unknown results remain
unavailable and no history/completeness ledger is fabricated. Manual/count pruning
continues to select retained history only.

## Retained attempt history

```sh
backlot history INSTANCE_ID --limit 100 --json
backlot history INSTANCE_ID --after CURSOR --json
backlot inspect INSTANCE_ID --last-completed --json
backlot inspect INSTANCE_ID --attempt ATTEMPT_ID --json
backlot history prune INSTANCE_ID --older-than 168h --keep 20 --dry-run --json
backlot history prune INSTANCE_ID --older-than 168h --keep 20 --json
```

History lists bounded summaries newest first by acceptance sequence, including the
current accepted attempt and retained predecessors. Page limit is 1–100, default
100; summaries keep encoded pages below the client's 1 MiB bound. Continuations
are live keysets bound to the instance, without a snapshot or replay guarantee.
Concurrent insertion may be missed; removal never redirects an exact selection.

`inspect --attempt` and `--last-completed` are mutually exclusive and return an
`AttemptResponse`, separate from ordinary current `InstanceResponse` inspection.
Last completed means the newest recorded completion, including failures, cancellation
and kept work; it ignores newer incomplete attempts. No completed record returns
`attempt_unavailable`. History is diagnostic data, never runtime recovery authority.
All accepted attempts and result writers participate; previously overwritten outcomes
cannot be reconstructed. Upgrades seed only the known current execution and known
launch provenance; unknown historical facts remain unknown. Legacy identities already
destroyed by an older daemon retain their known current result on upgrade; migration
and reads do not erase it. A matching marked destroy/retry performs the new erasure. `provenance` labels
new accepted execution (`accepted`), seeded current state (`legacy_current`), or
cleanup-only synthetic results (`cleanup_only`); cleanup does not invent a launch.

Prune requires at least one selector. `--older-than` takes a positive Go duration;
`--keep` takes a nonnegative completed count, including zero. When combined they
select the union. `--dry-run` reports selected attempt IDs, selector reasons, protection and evidence
bytes without effects; it is advisory and apply rechecks protection under the same
fences. Protected active/kept/cleanup-uncertain work is never deleted. Prune removes
selected history and evidence, leaving application data, credentials and private
snapshots intact. The current instance result remains current-state truth even if
its history row is pruned, with expired evidence; exact history stays unavailable
on later current-result updates. New attempts retain increasing acceptance order.
File deletion intent survives a crash or incomplete removal and is retried by the
existing retention sweep, without resurrecting metadata. Legacy mixed logs preserve
unselected records; rewriting visibly invalidates affected log cursors.

History features require matching client/daemon contracts (`attempt-history/v1`).
Unsupported old endpoints reject explicitly; clients never substitute the current
attempt. Successful destroy requires `DestroyRequest.contract` exactly
`erase-attempt-history/v1`, including force/retry. Matching CLI/client sends it.
A new daemon rejects absent/unsupported markers with `api_version`/426 before
admission or effects; an old strict daemon rejects the unknown field with
`invalid_document`/400. No marker-free fallback exists. Older clients must upgrade
to destroy on a new daemon or read its execution-free destroyed tombstones.
Lost/unusable mutation replies still mean unknown acceptance/completion.

## Portable per-instance export

`backlot export INSTANCE_ID --output NEW_DIRECTORY [--allow-partial]
[--attempt ATTEMPT_ID] [--wait[=SECONDS_OR_DURATION]] [--json]
[--state-dir DIRECTORY]` copies the selected settled execution attempt into a new caller-owned directory. For example:

```sh
backlot export INSTANCE_ID --output ./saved-run --wait=45s --json
# The entire directory can now be moved or uploaded.
open ./saved-run/index.html
```

The daemon owns selection, settlement, copying, audit and atomic publication.
The CLI sends an absolute destination; it never opens private state or artifact
paths. The parent directory must exist, belong to the caller, and be unwritable
by other users. Output must be outside private daemon state. Existing files,
directories and links are always rejected, including an empty directory. A sibling
private staging directory is renamed with the platform's atomic no-replacement
operation only after the report and all evidence pass audit. Failed/interrupted
exports remove their staging directory and never replace the destination.

Without `--wait`, active execution is rejected. Bare `--wait` observes indefinitely;
`--wait=45`, `--wait=45s` and `--wait=2m` set positive bounds. Interrupting export
does not cancel the run. SIGINT/SIGTERM, caller cancellation and wait deadlines
report `export_interrupted`, independently of daemon availability. All evidence
producers must settle, including services kept for investigation: explicitly stop
them, export the settled evidence, then destroy only if authorized. Unknown IDs
and instances with no execution are errors. Omitted `--attempt` selects the current
attempt. Exact selection requires a retained attempt and never falls back; a settled
older attempt can export while a replacement is active. Export uses the selected
launch provenance and original outcomes, identified in both outputs; it never
merges attempts. An unavailable/pruned selection is `attempt_unavailable`.

Strict default rejects known collection gaps, expired evidence and unavailable
retained artifacts. `--allow-partial` publishes an explicitly incomplete bundle
with omissions; it cannot recover pruned bytes or bypass path/link/key/archive
safety. A failed or cancelled application can have a complete, successful export.
The original exit (or absence), cancellation and execution/collection/cleanup
failures remain separate. A settled publication failure may return the observed
result in the typed export response alongside `error`, with `published: false`;
its CLI exit is nonzero and it supplies no usable output path. Actions accept
only complete published evidence.

`report.json` is the independent `backlot.export/v1` agent contract, defined by
`api/v1.ExportReport`. It contains bounded run/attempt identity, status, original
results, creation/completion/export timestamps, project/scene/lifetime, checkout
identity, available immutable launch Git commit (`source.git_head`) and manifest/config
digests. Symbolic branch names are not revisions; unborn or unavailable history
stays unknown. Historical
facts that were not captured remain absent/unknown. It excludes plans, environment,
fixture secrets, ownership capabilities, machine configuration and snapshots.
`complete` and `omissions` describe evidence availability. `evidence` inventories
copied files with relative `path`, kind, component/name, byte size and SHA-256.
Consumers must reject unsupported schema versions and resolve paths relative to
the report's directory. `ExportResponse` is the bounded CLI/API publication result,
not the full file inventory.

`index.html` is a script-free local-file summary of the same report, with escaped
untrusted text and relative evidence links. A collapsed-by-default, script-free
log section embeds up to 256 KiB of decoded text including record context. Larger
logs keep complete head/tail records within that display budget, state exact
formatted bytes/record counts omitted and link the complete original NDJSON.
Binary chunks are labelled and retain original base64. The summary and embedded
log preview are standalone; other application evidence remains relative. Application artifacts retain their
original bytes/assets and are explicitly labelled application-provided content;
those HTML reports may execute their own scripts and some need a local static
server. Use the producing application's viewer; Playwright traces can be opened
with a compatible installed `npx playwright show-trace PATH.zip`. Logs use typed
NDJSON with base64 for binary chunks. Keep the whole bundle together. It survives
destroy, daemon shutdown, private-state removal and relocation. The caller owns
export retention and upload, independent of daemon pruning.

The reusable generic audit rejects filesystem/archive links, special files,
private-key filenames and PEM private keys, unsafe ZIP paths, malformed ZIPs and
over-deep nested ZIPs (case-insensitive). Unsupported compressed/archive formats
are rejected. Safety bounds are 128 MiB per file or expanded archive entry,
512 MiB cumulative audited bytes (including expanded entries), and 10000 files/
entries (including every ZIP directory header, shared across nested and sibling
archives); these fail export explicitly rather than truncate copied evidence.
Application format assertions belong in application tests; Backlot does not parse framework reports or invent missing ones.
Applications remain responsible for credentials printed into logs or artifacts.
The same-user administrative socket remains the authorization boundary.

## Finite retained-log pages

```sh
backlot logs INSTANCE_ID --page --component tests --attempt ATTEMPT_ID \
  --stream stderr --limit 100 --json
backlot logs INSTANCE_ID --page --component tests --attempt ATTEMPT_ID \
  --stream stderr --after CURSOR --limit 100 --json
```

`--page` emits one `LogPageResponse`; ordinary logs retain LogRecord-only NDJSON.
Page conflicts with follow; attempt, stream, after and limit require page. The
record limit is 1–1000 (default 100). Each page scans at most 512 KiB and returns
at most 512 KiB of encoded records; filtered-out records consume the scan budget.
An empty filtered page can advance `continuation`. `more` means unscanned bytes
at the observed source sizes, including an incomplete active tail, or later sources;
`active` retains existing runtime/kept/cleanup-uncertain semantics.

Resume with the same instance/component/attempt/stream filters. Limit may change.
The bounded opaque cursor is caller-held metadata bound to daemon generation,
query, retained source identity and complete-record byte position. At EOF it can
be used for an explicit later read; there is no subscription or durable replay.
An active partial record stays unread; a settled incomplete/corrupt record fails.
A partial tail can return the same continuation while awaiting more bytes.

Order is lexical retained filename then physical record order, never timestamp
or causal order. Appends can appear on subsequent requests; new earlier sources
may be missed. Enumeration scales with retained filenames. Separate pages are
live, with no snapshot, restart, retention or private-state-tampering guarantee.
A new daemon generation fails explicitly. Missing/replaced/truncated cursor
sources report structured `source_changed` and require a fresh query.

`gaps` independently reports `retention_expired`, `collection_incomplete` and
`source_changed`, with safe detail and existing native ownership warnings. Exact `--attempt` selection uses its retained retention/collection facts and reports
`historical_collection: "known"`; collection gaps name that attempt. Missing/pruned
selection returns `attempt_unavailable`. Aggregate pages retain current-state gaps
and `historical_collection: "unknown"`, since they do not claim a combined ledger.
No records and no known gap never prove complete capture. Transport/corruption
errors remain nonzero, distinct from successful partial-evidence pages.
