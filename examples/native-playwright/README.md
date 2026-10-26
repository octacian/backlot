# Native Playwright over loopback HTTP

This adopter recipe uses a native Node server and native Playwright 1.64.1
Chromium. Backlot gives each invocation a dynamic loopback port, forwards the
terminal argument vector and retains `reports/` before stopping the scene.
No gateway, certificate, container or browser shard matrix is needed.

From the repository root, build Backlot with `make build`. Install application
dependencies separately, before starting a daemon that will run this project:

```sh
cd examples/native-playwright
npm ci --ignore-scripts --no-audit --no-fund
export PLAYWRIGHT_BROWSERS_PATH="$PWD/.browsers"
export PLAYWRIGHT_SKIP_BROWSER_GC=1
npx --no-install playwright install chromium
```

Linux machines also need Playwright's documented browser OS prerequisites.
The example pins Node 26.8.1. Backlot does not install application dependencies.
If using `PLAYWRIGHT_BROWSERS_PATH`, set it before launching the fixture daemon.

Choose a new private absolute directory for `STATE`, outside the checkout, and
an existing private absolute directory for `EVIDENCE`. Use the built binary's
absolute path as `BACKLOT`:

```sh
"$BACKLOT" daemon start --state-dir "$STATE" --json
"$BACKLOT" run browser --project "$PWD" --state-dir "$STATE" --json -- --grep 'counter interaction'
```

The command after `--` goes straight to Playwright, which owns selection and
workers. The retained `reports/invocation.json` records its argument vector and
resolved URL. There is no intervening npm/shell/test-runner wrapper. Replace
`app.mjs` and the test with your application while preserving the explicit port
mapping, readiness dependency and output group.

Save the returned instance ID as `ID`. Assertion failure preserves the original
terminal exit separately from infrastructure, collection and cleanup errors.
Interrupting the live `backlot run` cancels its disposable execution. The pinned
runner can retain partial traces without final Playwright HTML/JSON or screenshots;
see the [observed cancellation limitation](../../docs/evidence.md#native-runner-cancellation).

After the execution settles, export to a new destination before removing private
fixture state:

```sh
"$BACKLOT" export "$ID" --output "$EVIDENCE/$ID" --wait=45s --state-dir "$STATE" --json
"$BACKLOT" destroy --state-dir "$STATE" --json "$ID"
"$BACKLOT" daemon stop --state-dir "$STATE" --json
```

Inspect every command's result. A cleanup error requires retry/diagnosis with the
same state; retain uncertain state. The exported `index.html`, logs, Playwright
HTML/JSON, screenshots and traces, when produced, can outlive the daemon. Strict export rejects
known collection gaps rather than inventing reports. Keep the bundle together;
open traces with the matching installed Playwright viewer.

This directory is the application template. Repository lifecycle, signal and
evidence-format verification belongs outside it; the existing managed HTTPS
example remains [separate](../ci-scenes/README.md).
