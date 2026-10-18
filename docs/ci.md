# GitHub Actions quickstart

Use [`actions/setup`](../actions/setup/action.yml) on **ephemeral Ubuntu 24.04
amd64** hosted runners for a native application and Playwright Chromium. Copy
[the adopter workflow](../examples/ci-scenes/adopter.yml) to your application's
`.github/workflows/browser.yml`, review/update the pinned action commit, and commit your application's npm lockfile.
The [runnable application](../examples/ci-scenes/README.md) supplies the matching
[manifest](../examples/ci-scenes/backlot.yaml), [browser launcher](../examples/ci-scenes/scene.mjs)
and [Playwright configuration](../examples/ci-scenes/playwright.config.mjs).
Copy these three small files and adapt the manifest build/service commands,
readiness probe, output paths, `PORT`, `APP_DATA` and application origin settings.
Keep your application's own tests and dependency/build steps. The example's
`ci.mjs`, faults, signal checks and acceptance harness are product verification;
they are not part of the adopter workflow.

The terminal launcher forwards arguments unchanged to Playwright, including
`--shard=1/2` and `--shard=2/2`. Backlot creates one fresh origin/port/data directory
per scene. Playwright schedules workers inside that scene: applications must
separate test accounts, keys or database schemas between workers. Output
concurrency groups serialize builds/reports sharing one daemon and checkout;
use independent worktrees for parallel stacks, and never use multiple daemons
against one writable checkout.

## Installation, versions and permissions

The action requires **exact Go 1.27.1**, Git, `lsof`, and an absolute application
Node executable (`node-binary`). A mismatch fails explicitly; no automatic
installation fallback occurs. Pinned `actions/setup-go` in the workflow installs
Go; cache its immutable module downloads and build data only. Non-Go consumers
use explicit `actions/cache` paths as shown. npm, application Node, Chromium,
`libnss3-tools` and browser OS libraries stay workflow-owned and configurable.
The action itself runs on the runner's **Node24 Actions runtime**; this is separate
from the example application's Node 26.8.1 and pinned Playwright 1.64.1.

`backlot-ref` is a required full lowercase 40-character commit SHA. The action
fetches only `https://github.com/octacian/backlot.git`, verifies the checked-out
commit and Go module identity, downloads/verifies modules with the public Go
checksum database, and builds a static trimmed binary. It installs **stock Caddy
v2.11.6** through the Go proxy/checksum database and verifies the executable's
main module path/version and absence of module replacements. Failed fetches,
checks or builds fail setup. No unpublished release assets are assumed: issue
[#25](https://github.com/octacian/backlot/issues/25) owns checksum-verified release
asset integration. Source installation remains explicit until then.

The SHA after `uses: octacian/backlot/actions/setup@…` selects **action code**;
`backlot-ref` selects the **Backlot executable** independently. Pin both and
review upgrades separately. `actions/setup` is the stable path; use full SHAs
now. No action version tags or release compatibility promises are created by
this change; future action tags must describe action contract changes separately
from Backlot releases. The manifest remains `backlot/v1`.

Only `permissions: {contents: read}` is needed for checkout. The public action
source fetch uses no token; artifact/cache actions use their normal job-scoped
service credentials. No repository write, OIDC, secret or administrator privilege
is needed by Backlot setup. Workflow apt/browser dependency installation uses
the disposable hosted runner's sudo. Self-hosted runners are outside this slice.
Never cache/upload private job state, CA directories, or authenticated sockets.

## Environment and lifecycle contract

Inputs are `backlot-ref` and `node-binary`. Outputs are `binary`, `config`,
`state-dir`, `helper` and elapsed `setup-seconds`. Setup adds `backlot` to PATH
and exports `BACKLOT_BINARY`, `BACKLOT_CONFIG`, `BACKLOT_STATE_DIR`,
`BACKLOT_SETUP_HELPER` and a private `BACKLOT_SETUP_OWNER` cleanup capability.
Do not print or upload that capability. The short mode-0700 `/tmp/bl-ci-*` root
holds downloads, config, daemon state and private CA. Dynamic unprivileged HTTPS
ports and loopback-only administration avoid existing services. A port collision
fails without takeover. The action registers its bounded native post hook before
allocating a daemon/gateway; setup errors attempt scoped cleanup immediately.
Uncertain cleanup fails and preserves state for authenticated diagnosis.

The action starts only the job daemon/gateway. The workflow explicitly runs
`backlot run`, then **collect → upload → final cleanup** with `always()`.
Do not use `|| true` on tests or fold cleanup into their exit status. Backlot
retains the original terminal exit separately from infrastructure, collection
and cleanup failures. Cancellation propagates through the CLI's supervised
clients; the 3-second disconnected-client lease bounds detached disposable work.
The action's post hook is a final cleanup fallback, never an evidence-upload
replacement. Abrupt VM destruction cannot guarantee upload or post execution.

Before export, collection waits at most 45 seconds for disposable runs to settle
(including client lease expiry after runner cancellation). It never waits for
persistent services to exit or starts new work. A timeout fails collection without
publishing partial final results; final cleanup still runs. Collection uses an allowlist of result fields, application logs and declared
artifacts under daemon evidence storage. It rejects symlinks, special files,
private-key filenames and PEM keys, including ZIP contents, before publishing
staging. It excludes daemon snapshots, capabilities, CA storage and browser NSS
state. Application outputs still need application-level secret discipline.
Inspect each artifact's `result.json`, `logs.ndjson` and reports/HTML/trace files;
collection errors remain failed steps and final cleanup still runs.

## Trust and troubleshooting

The action gives only the daemon explicit private Go root settings for HTTPS
readiness (`SSL_CERT_FILE` and an empty private `SSL_CERT_DIR`). Applications and
tests must map the origin's `ca_cert_path` to `NODE_EXTRA_CA_CERTS` in the
manifest. Node reads this at startup; explicit client `ca` options must include
it deliberately. The launcher imports only the public root into an instance-owned
Chromium NSS database and supplies a disposable browser HOME/profile. See
[application trust](https.md#explicit-application-trust). No global trust or DNS
mutation, TLS-ignore flags, or browser warning bypass is used.

- Bootstrap failure: check pinned SHA, exact Go version, proxy/network access,
  `lsof`, Caddy build identity and the action log. Failed setup publishes no usable
  daemon outputs; the native post hook still checks its recorded ownership.
- DNS failure: verify ordinary `getent hosts bl-check.backlot.localhost`; use a
  supported runner resolver. Do not substitute URLs, hosts files or resolver flags.
- TLS failure: check the explicit mapping separately for daemon, SSR, Node tests
  and browser. A trusted Node client does not establish browser trust.
- Cleanup failure: inspect the failed step and retained private root locally in
  the disposable job. Retry only with the recorded capability and daemon API;
  never scan/kill by PID/name, prune resources, or upload the private root.

[The automatic scenes workflow](../.github/workflows/scenes.yml) exercises both
shards through this action and reuses failure/recovery, trust and lifecycle checks.
Revision-bound independent-consumer evidence is recorded with the delivery;
these examples do not establish broader macOS/provider/release readiness.
