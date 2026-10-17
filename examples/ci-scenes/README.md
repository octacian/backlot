# Disposable application scenes on Ubuntu

This is an application CI example: a native Node notes application, an
instance-owned directory, stock managed Caddy, and a disposable Playwright
Chromium job. [The automatic workflow](../../.github/workflows/scenes.yml) runs
two independent shards on every PR and `main` push. It adds no Backlot scheduler
and does not repeat the existing Linux installation/Docker/provider suites.

The bounded combination is Ubuntu 24.04 **amd64**, Go 1.27.1, Node 26.8.1,
Caddy 2.11.6, and `@playwright/test` 1.64.1's Chromium headless shell
156.0.8078.4 (revision 1248). The lockfile pins the browser driver/download
revision. Both shards passed the initial real Ubuntu checkpoint at `1fef82b`
([run 38982798894](https://github.com/octacian/backlot/actions/runs/38982798894));
this is development evidence, with independent reviews and frozen final gates
still outstanding. Other engines,
distributions, and containerized browsers are not claimed.

## Bootstrap and run

Use an ephemeral Ubuntu 24.04 amd64 runner with its ordinary `.localhost`
resolver, Git, Make, Go, Node, Python 3 (evidence audit), and a C compiler. The workflow installs `lsof`,
`libnss3-tools`, and Chromium's OS libraries with apt, builds stock Caddy with
`go install github.com/caddyserver/caddy/v2/cmd/caddy@v2.11.6`, and builds the
static Backlot executable with `make release-build RELEASE_GOOS=linux
RELEASE_GOARCH=amd64`. Run `npm ci --ignore-scripts` and `npx --no-install
playwright install --with-deps chromium` in this directory. The workflow caches
Go modules/builds, npm downloads, Caddy, and the pinned Playwright browser
downloads; apt dependencies remain explicit on each fresh runner.

Set these **absolute**, job-owned paths (the export path and pointer must not
already exist). The short private root itself is created under `/tmp` to fit
Unix socket path limits:

```sh
export BACKLOT_BINARY="$PWD/bin/backlot-linux-amd64"
export BACKLOT_CADDY_BINARY="$RUNNER_TEMP/scene-tools/caddy"
export BACKLOT_CI_POINTER="$RUNNER_TEMP/scene-root"
export BACKLOT_CI_EXPORT="$RUNNER_TEMP/scene-evidence"
export PLAYWRIGHT_BROWSERS_PATH="$RUNNER_TEMP/playwright-browsers"
node examples/ci-scenes/ci.mjs bootstrap
node examples/ci-scenes/ci.mjs run --shard=1/2
node examples/ci-scenes/signal-check.mjs
node examples/ci-scenes/ci.mjs acceptance
node examples/ci-scenes/ci.mjs collect
# Upload only BACKLOT_CI_EXPORT here, before runner teardown.
node examples/ci-scenes/ci.mjs cleanup
```

`run` first exercises the selected real browser shard, then an intentional
Playwright assertion failure (exit 1), then the selected shard again to prove
recovery. It asserts each original terminal exit independently of collection
and cleanup failures. `scene.mjs` forwards every supplied argument directly to
Playwright; the retained `invocation.json` proves the exact vector, including
`--shard=x/y`. For a single scene without these checkpoint assertions, use the
normal `backlot run browser --project examples/ci-scenes --config <private
machine.json> --state-dir <private state> --json -- --shard=1/2` command.

`acceptance` runs once in shard 1. It holds a real browser while a second run
queues on the same checkout, verifies no build/report overwrite, cancels the
first CLI with SIGINT (exit 130), and lets the second run execute both specs
with two workers. It then creates a real independent Git worktree with its own
dependency install, runs two browser stacks simultaneously, and checks distinct
origins, ports, directory paths and stored stack identities. A note written in
one remains absent in the peer. Killing only one retained CLI handle proves
daemon lease cancellation, collected evidence, removed data and route, while
the peer stack/data and an unrelated listener remain intact. The final peer
is cancelled and the worktree is removed during authenticated runner cleanup.

`signal-check.mjs` signals the real outer `ci.mjs acceptance` process with
SIGINT and SIGTERM during its no-command wait with a held browser and a queued
client. It requires outer exits 130/143, both clients joined with their own
signal results, no subsequent scene or worktree allocation, available held
browser evidence, removed data/routes and an unrelated listener surviving.
The runner tracks all live CLI clients, rejects new commands after cancellation,
interrupts acceptance waits and joins clients before exiting in every mode.
Native terminal results remain separate from the outer and CLI signal exits.

## Trust and isolation

Bootstrap starts a private daemon with `SSL_CERT_FILE` pointing to the job's
public Caddy root and `SSL_CERT_DIR` pointing to an empty private directory.
The manifest separately assigns `NODE_EXTRA_CA_CERTS` to the outbound SSR
application and the Node/Playwright test runner. The terminal launcher creates
an NSS database in the instance directory and imports **only the public root**;
Chromium uses a disposable HOME and profile. Every shard rejects the same origin
with a fresh untrusted browser and a Node child lacking the explicit root,
then verifies trusted canonical navigation, Node API requests, and native SSR.
No `caddy trust`, system trust write, resolver change, host mapping, public
certificate credential, TLS-ignore flag, or warning bypass is used. A runner
whose ordinary native DNS cannot resolve `*.backlot.localhost` fails the gate.

Each disposable invocation gets a distinct origin, port, and data directory.
Each matrix job has its own checkout, gateway/CA, outputs, and artifact name.
The `application` checkout output group covers both `build/` and `reports/`
through collection: two runs sharing one daemon and checkout serialize, so
building or clearing reports cannot race a live consumer. Independent worktrees
can run concurrently. Do not point separate daemons at the same checkout and
expect cross-daemon output locking. Do not run `npm ci` against a live checkout.

Playwright schedules files across shards and workers. Backlot isolates a scene,
not the test workers **within** it. The application tests use worker/test-specific
note keys; real applications must likewise isolate accounts, DB schemas, tenant
IDs, or transactions. Each new scene starts with fresh data. Browser tests cover
rendering, real persistence interaction, loading, a real application HTTP 503,
retry recovery, and a 360px viewport.

## Evidence and teardown

Backlot copies `reports/` before disposing of the scene. The runner exports
allowlisted result fields, retained application NDJSON logs, `invocation.json`,
Playwright JSON/HTML, screenshots and ZIP traces. Raw daemon state, snapshots,
gateway configuration, NSS databases, and CA directories/keys are never upload
sources. The exporter rejects links, special files, key filenames and PEM private
keys (including ZIP entries), validates JSON trace records and PNG headers, and
publishes only audited staging. Application logs/artifacts still require
application-level secret discipline. CI collection, upload, and authenticated
cleanup are `always()` steps, so an expected or unexpected test failure does not
skip evidence or teardown. Actions cancellation allows these bounded steps;
an abruptly destroyed VM cannot promise a final upload. Client lease expiry
bounds disconnected disposable work independently of the runner.

Cleanup uses recorded instance IDs and the authenticated daemon, stops the
owned gateway/daemon, and deletes the private root only after verified success.
Any uncertainty fails and retains the root for diagnosis. It never scans/kills
process names, prunes Docker, or changes operator services. The existing
[disposable runtime gates](../../docs/testing.md#dockerprivate-state-integration)
remain required alongside this application's browser checks.
