# GitHub Actions quickstart

For a smaller local adopter starting point, use [native Playwright over loopback
HTTP](../examples/native-playwright/README.md) or [native Go tests against a
disposable MariaDB](../examples/native-go-mariadb/README.md). Their manifests and
application commands are separate from repository fault/evidence verification.
Early real success/failure/recovery and a bounded cancellation check have been
observed; see [the native runner limitation](evidence.md#native-runner-cancellation).
The conditional four-LTS fresh-runner cost checkpoint remains pending. The recipes
do not replace the managed HTTPS browser gate below.

Copy [the adopter workflow](../examples/ci-scenes/adopter.yml) into your application
repository for **ephemeral Ubuntu 24.04 amd64** hosted runners. Install your own
application Node, npm dependencies and browser first, then use
[`actions/quickrun`](../actions/quickrun/action.yml) to run one disposable scene.
It prepares private Backlot/Caddy, runs the scene, collects and audits its evidence,
uploads an artifact by default, and removes its private job runtime. A failed test
still fails the action after evidence and cleanup are attempted.

The [existing application](../examples/ci-scenes/README.md) supplies a matching
[manifest](../examples/ci-scenes/backlot.yaml), [browser launcher](../examples/ci-scenes/scene.mjs)
and [Playwright configuration](../examples/ci-scenes/playwright.config.mjs).
Adapt the build/service commands, readiness probe, output paths, `PORT`, `APP_DATA`
and origin mappings to your application. Keep your own tests and lockfile.
The example's acceptance/fault/signal harness is product verification, outside the
adopter workflow. Shards and Playwright worker isolation remain application-owned;
these actions do not schedule shards.

## Installation and independent versions

For now, install **exact Go 1.27.1** using pinned `actions/setup-go` with
`cache: false` before Backlot setup/quickrun. This single explicit toolchain step
keeps source installation clear while the native actions preserve their bounded
post hooks. Backlot encapsulates Go module/build caching through the official
Actions cache SDK; workflows need no Backlot cache paths or keys. Cache failures
are warnings, while fetch, checksum, identity and build failures fail setup.
Only Go module/build caches are cached; runtime state, capabilities and CA storage
are never cached or uploaded.

Setup owns `lsof`, installing it with apt/sudo if absent on the bounded runner.
Git and Go must be available. Application Node defaults to the executable named
`node` on **PATH**, separately from the Node24 Actions interpreter. Set optional
`node-binary` to an absolute executable to override it. Application Node versions,
npm/build/test dependencies, Chromium, browser OS dependencies and `libnss3-tools`
remain workflow-owned. The example pins Node 26.8.1 and Playwright 1.64.1; these
are example choices, not versions installed by Backlot.

Pin both the action `uses: octacian/backlot/actions/quickrun@<full SHA>` and required
`backlot-ref: <full lowercase 40-character SHA>`. The former selects action code
while the latter independently selects the Backlot executable, which owns public
export and generic evidence auditing. Both pins must select compatible revisions
containing the issue #41 public export contract.
Setup fetches only `https://github.com/octacian/backlot.git`, verifies the source
commit/module, downloads/verifies Go modules through the public checksum database,
and builds static Backlot. Stock Caddy **v2.11.6** is installed with `go install`
and its module identity/version/replacements are verified. No release binary or
silent fallback is used. [Issue #25](https://github.com/octacian/backlot/issues/25)
owns release integration, including future helper packaging without Go.

An older runtime fails with a clear unsupported-export error; there is no
private-storage fallback or migration framework. These native actions share modules from the downloaded action repository, so remote sibling
actions work even when the application checkout contains no Backlot files.
`permissions: {contents: read}` suffices; cache/artifact SDKs use normal job-scoped
credentials. No OIDC, repository writes or administrator token is needed.
Self-hosted runners and action version tags are outside this slice.

## Setup plus sequential runs

Use [`actions/setup`](../actions/setup/action.yml) once when intermediate steps or
multiple scenes need the same job daemon/gateway, then
[`actions/run`](../actions/run/action.yml) for each disposable scene:

```yaml
- uses: octacian/backlot/actions/setup@<reviewed-action-SHA>
  with:
    backlot-ref: <reviewed-runtime-SHA>
- uses: octacian/backlot/actions/run@<reviewed-action-SHA>
  with:
    project: .
    scene: browser
    args: '["--shard=1/2"]'
- uses: octacian/backlot/actions/run@<reviewed-action-SHA>
  with:
    scene: browser
    args: '["--grep", "checkout flow"]'
```

`args` is an exact JSON array of strings, forwarded without shell parsing.
`project` defaults to `.`; `scene` is required and must be disposable. Run collects
and destroys only its returned invocation ID, leaving earlier history, peer
instances and shared daemon/gateway available. Setup's native post hook performs
bounded final job cleanup. Quickrun uses the same implementation and performs
that private cleanup immediately before returning, with a native post fallback.
Do not use independent daemons against the same writable checkout.

Setup exports `BACKLOT_BINARY`, `BACKLOT_CONFIG`, `BACKLOT_STATE_DIR`,
`BACKLOT_SETUP_HELPER` and a private ownership capability
`BACKLOT_SETUP_OWNER`. Never print/upload that capability or private state.
Its mode-0700 `/tmp/bl-ci-*` root contains downloads, authenticated sockets and CA
keys. Ownership is registered for post cleanup before downloads or daemon startup;
setup failures attempt cleanup immediately. Uncertain ownership is preserved and
reported rather than killed by PID/name or hidden by deleting state.

## Evidence, custom upload and results

Run/quickrun default to `upload-artifacts: 'true'` using the official Actions
artifact SDK. Optional `artifact-name` is a prefix; invocation ID and a unique
suffix prevent collisions. Each invocation exports to a unique directory under
`RUNNER_TEMP`, **outside private runtime state**, before destroying its record.
Quickrun's exported evidence remains readable after its daemon/gateway/private
root are removed. For your own uploader later in the **same job**:

```yaml
- uses: octacian/backlot/actions/quickrun@<reviewed-action-SHA>
  id: tests
  with:
    backlot-ref: <reviewed-runtime-SHA>
    scene: browser
    upload-artifacts: 'false'
- uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4
  if: always() && steps.tests.outputs.evidence-available == 'true'
  with:
    name: application-evidence
    path: ${{ steps.tests.outputs.evidence-path }}
    if-no-files-found: error
    include-hidden-files: true
```

`evidence-available` becomes `true` only after full audit and publication;
`evidence-path` is absent on failed collection/audit. Collection waits at most
45 seconds for final disposable results; it never publishes partial results.
Known runtime collection failures, expired evidence and native log-gap diagnostics
prevent publication and upload, while successfully collected evidence remains
available after an assertion failure or clean cancellation.
Run calls `backlot export INSTANCE --output NEW_DIRECTORY --wait=45s --json`.
The daemon owns report construction, completeness, safety audit and publication;
the action handles upload, outputs, signals and authenticated cleanup. The bundle
contains `report.json` (`backlot.export/v1`), `index.html`, logs and copied retained
artifacts; see [the portable contract](evidence.md#portable-per-instance-export).
Application-format assertions live in the scenes fixture.

Pin `backlot-ref` to a full reviewed revision containing issue #41 public export,
and select an action revision using that contract. An older runtime fails with
an actionable unsupported-export error; there is no fallback to private storage.
The [release archive workflow](testing.md#release-packaging-and-publication)
prepares versioned binaries separately; this action still builds its selected
source revision. No tag or release publication is implied.

Outputs keep the original `cli-exit-code`, `cli-signal`, `wrapper-signal`,
`terminal-exit-code`, `status`, `cancelled` and `instance-id` separate from
`runtime-failure`, `runtime-collection-failure`, `runtime-cleanup-failure` and
wrapper `infrastructure-error`, `collection-error`, `upload-error`, `cleanup-error`,
`job-cleanup-error`. Missing native results stay absent. Original nonzero CLI exits
and cancellation are preserved; other collection/upload/cleanup failures fail the
action without rewriting the test result. A signal reaches the retained actual
CLI and the wrapper joins it, preventing an abandoned lease-renewing child.
If no final instance identity is returned, run preserves uncertain state for the
setup post hook rather than discovering/destroying other invocations.

Use `always()` and the availability output for custom upload after failure or
cancellation. Runner hard termination can interrupt collection; native post remains
a bounded private-cleanup fallback. A destroyed VM cannot promise outputs, post
execution or upload. Exports last only for that job unless uploaded. Preserve audited hidden files when
uploading a bundle (`include-hidden-files: true` with upload-artifact); otherwise
the uploaded directory may no longer match its report inventory.

## Trust and troubleshooting

Only the daemon gets private Go root settings for HTTPS readiness (`SSL_CERT_FILE`
and an empty `SSL_CERT_DIR`). Applications/tests must explicitly map origin
`ca_cert_path` to `NODE_EXTRA_CA_CERTS` in the manifest. Node reads it at startup;
explicit client `ca` options must include it deliberately. The launcher imports
only the public root into an instance-owned Chromium NSS database and uses a
disposable browser HOME/profile. See [application trust](https.md#explicit-application-trust).
No global trust/DNS changes, TLS-ignore flags or browser warning bypass are used.

For bootstrap errors check exact Go, pinned source, network/checksums and Caddy
identity. DNS requires ordinary `getent hosts bl-check.backlot.localhost` resolution.
Check daemon, SSR, Node tests and browser trust separately. For cleanup uncertainty,
retain private state locally for authenticated diagnosis; never prune unrelated
resources or upload the private root.

[The scenes workflow](../.github/workflows/scenes.yml) exercises sequential setup/run
and existing browser/trust/fault/lifecycle checks. The
[independent consumer](#independent-action-consumer) checks action packaging.
New revision-bound evidence and remaining gates are recorded
with the delivery; these examples make no broader release/provider readiness claim.


## Private project inputs in setup and quickrun

Both actions accept `project` (default `.`), `project-inputs` (strict JSON object
of nonempty string values) and `project-generate-inputs` (explicit JSON array of
eligible secret names). They invoke existing atomic `project setup` directly over
stdin; values never become shell text or CLI arguments. For example:

```yaml
- uses: ./actions/quickrun
  with:
    backlot-ref: ${{ github.sha }}
    project: application
    project-inputs: ${{ format('{{"region":"west","credential":{0}}}', toJSON(secrets.APP_CREDENTIAL)) }}
    project-generate-inputs: '["session-token"]'
    scene: test
```

Construct JSON with an expression such as `toJSON` when values can contain quotes
or newlines; do not interpolate unescaped secret text into JSON. Both options
unset skip enrollment and manifest discovery; explicit `{}` or `[]` requests
input-free enrollment. Invalid/duplicate/unknown names, invalid shapes, empty
values and incomplete candidates fail atomically. Supplied values are masked
before potentially revealing work; generated secrets are captured through local
explicit inspection and masked before runtime activation. Sensitive failures use
bounded captures and fixed diagnostics. The action suppresses raw setup/inspection
transport even when masks exist.

Every Backlot child, including daemon, direct run, export and post cleanup, selects
an authenticated external job registry using `BACKLOT_PROJECT_CONFIG_DIR`.
HOME/XDG and unrelated tool configuration stay unchanged; raw structured payload
environment keys are removed from children. Existing `version --json` API/state
metadata must advertise v1/state8 before any configuration resolution, even when
project setup was not requested. Older runtime pins fail rather than silently
ignoring these options or consulting default storage.

Successful cleanup stops consumers then removes the owned private registry,
generations and snapshots with the job root. Never upload/cache these private
files or the input payload. Public evidence survives cleanup. Existing declared
secret redaction remains in force; public inputs may appear as public plan values.
Applications remain responsible for secret values they print or write to logs,
reports and artifacts. Workflow masks are a log backstop and do not sanitize
arbitrary artifact bytes. The current-head project-inputs workflow exercises the
new setup/run/quickrun contract. The independent consumer checks packaging only;
it does not repeat this input/failure/lifecycle suite.


## Independent action consumer

[Setup action consumer](../.github/workflows/setup-consumer.yml) is a manual-only
packaging smoke test. Dispatch it on the branch being verified. GitHub's
[`$/` action reference](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#example-using-an-action-in-the-same-repository-as-the-workflow-at-the-running-commit-recommended)
downloads `actions/quickrun` at the workflow's running commit without checking out
Backlot into the application workspace. `backlot-ref: ${{ github.sha }}` selects
the same runtime revision. There are no historical action/runtime pins.

The workspace contains a separate minimal application Git repository with no
Backlot sources or actions. One native Node job writes an artifact; the workflow
requires successful action outputs and the expected artifact in the exported
bundle. This catches missing packaged sibling modules and accidental dependencies
on the application's checkout. The regular scenes and project-inputs workflows
own browser, trust, failure/cancellation, reuse, cleanup, upload and input coverage;
the consumer does not repeat those suites. External adopters should continue to
pin reviewed SHAs as described above.
