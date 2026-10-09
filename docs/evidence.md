# Disposable results and evidence

`backlot run <scene> -- <arguments>` forwards the final argument vector directly
to the disposable scene's designated terminal job. Other components receive only
their declared arguments. Arguments containing spaces remain single arguments;
Backlot performs no shell expansion.

On an otherwise clean run, the executable returns the terminal job's exact exit
code. SIGINT and SIGTERM request bounded cancellation and return 130 and 143.
An orchestration, collection or cleanup failure returns a generic nonzero code;
the original terminal status remains in `execution.terminal_exit_code` and the
component result. Unknown job exit status remains absent; observation or collector
failures never substitute an application exit code. Later verified status recovery
preserves the observed code alongside the separate failure. `failure`, `collection_failure`, `cleanup_failure` and
`cancelled` describe separate outcomes. Finite JSON commands emit one typed result;
application output is available through `logs`. Backlot captures application
stdout/stderr as emitted, including any secrets the application prints. Backlot's
own diagnostic messages redact resolved sensitive values.

Disposable clients renew a finite lease while waiting. Losing the client stops
renewals; the daemon cancels after the configured lease expires, gathers available
evidence, and cleans up. Execution deadlines of zero do not disable the lease.

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
Files and directories are copied into private daemon storage before containers
or data are removed. Links and special files are rejected; paths cannot traverse
outside the checkout or extraction directory. A declared missing source or failed
copy sets collection failure and prevents successful overall execution. Successful
copies appear in `execution.artifacts` with component, name, attempt and local path.
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

Only `secret` and its dedicated `/v1/fixtures/secret` endpoint return a named
sensitive fixture's value. `/v1/fixtures` returns ordinary metadata. The local
permissioned administration socket is the authorization boundary; do not share
sensitive command output or private daemon state.

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
Explicit destroy removes owned runtime/data after verified stop. Both preserve
historical results and evidence. Daemon crash recovery stops only verifiably owned
survivors, retains data/evidence, reports interruption/collection gaps and requires
an explicit restart. Unknown ownership remains untouched with a cleanup failure.

## Logs and retention

Historical and following logs read the same retained records outside native groups
and containers. JSON following emits typed NDJSON records with instance, component,
attempt, timestamp, stream and original output chunk. Non-UTF8 chunks use base64
`data`. Component filters preserve the unfiltered cursor. Human logs include the
same context. Collection and interruption gaps are reported explicitly.

Daemon-wide evidence defaults to 168 hours and 1 GiB. Configure the daemon at
startup (independent of scene machine configuration):

```sh
backlot daemon start --state-dir /private/backlot --retention-age 168h --retention-bytes 1073741824
```

The daemon periodically prunes oldest completed evidence first when age or total
size exceeds the limits. Active evidence, including kept investigation runtime,
is protected even when it alone exceeds the size cap. Pruning never removes
runtime or retained data. Expired result metadata remains discoverable with
`execution.evidence_expired`; logs report that the evidence expired. A committed
expiration with incomplete file removal is retried on subsequent sweeps/restart.
Destroyed instances follow the same policy. Private snapshots and named fixture
metadata are durable instance state, separate from retained log/artifact bytes.

Completed legacy results without a recorded completion time start their retention
age at the upgraded daemon's first observation. That inferred timestamp
is persisted as `execution.evidence_observed_at` once; daemon restarts do not reset
it, and old file mtimes do not cause immediate age expiration. The size cap still
applies. Active, kept and cleanup-uncertain runtime remains protected.
