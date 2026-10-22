# macOS HTTPS quickstart

Run this self-contained frontend, API, native Node SSR and disposable test through
one trusted HTTPS origin. Target: macOS ARM64, stock Caddy 2.11.6 and Node 26.
No Docker, public domain or DNS/resolver configuration is needed. Node/npm and
`ws` are dependencies of this example application; Backlot contributor tooling
remains Go-only. `ws` is pinned in the lockfile and is [MIT licensed](https://github.com/websockets/ws/blob/master/LICENSE).
[Install Backlot first](../../README.md#install-backlot).

## Prepare once

Install stock Caddy and Node separately, for example `brew install caddy node`.
Use Node 26 or newer for this example. Go installation supplies only the Backlot
command, so obtain the example source with Git (skip the clone if you already have
a Backlot checkout):

```sh
git clone https://github.com/octacian/backlot.git
cd backlot
```

From the Backlot checkout root:

```sh
cd examples/macos-quickstart
npm ci --ignore-scripts
umask 077
mkdir -p "$HOME/.config/backlot"
cp machine.example.yaml "$HOME/.config/backlot/machine.yaml"
```

Edit that private machine file to use the absolute path from `command -v caddy`;
if necessary add `tools: {node: /absolute/path/to/node}`. Defaults use loopback
ports 2020 and 8443; check `lsof -nP -iTCP:2020 -iTCP:8443 -sTCP:LISTEN` before
starting. If occupied, select two other unprivileged ports in the file, without
stopping their owner. Use the configured admin port in the trust command below.

```sh
backlot daemon start --state-dir "$HOME/.backlot" --json
backlot gateway start --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
/opt/homebrew/bin/caddy trust --address 127.0.0.1:2020 --ca local
```

Run that **stock trust command yourself** against your owned gateway; it can ask
for administrator authorization. Backlot never installs trust. System trust is
needed by the daemon's aggregate HTTPS readiness, independently of application
and browser trust. If the daemon has already attempted an untrusted HTTPS request,
refresh its trust cache: `backlot daemon stop --state-dir "$HOME/.backlot"`, then
repeat daemon start and gateway start above (CA identity is retained).

A browser with a separate certificate store also needs the public certificate
`$HOME/.backlot/gateway/data/pki/authorities/local/root.crt` imported as a trusted
certificate authority. In BrowserOS Neo, open certificate management from browser
settings, import this public file into its trusted authorities/local certificates,
and enable website trust if prompted. Verify the actual origin without a warning;
browser versions/policies differ. Keep signing keys and the remaining CA directory
private. [Trust and lifecycle details](../../docs/https.md#managed-local-https).

## Seeded demo account

Register the application email once from this example directory, using the same
private machine file as above:

```sh
backlot project setup --project . --config "$HOME/.config/backlot/machine.yaml" --from - --json <<'JSON'
{"version":"backlot/v1","inputs":{"email":"demo@example.invalid"}}
JSON
```

The manifest maps the email input and a generated password into the `seed` job
and its fixtures. The application stores the demo account in its private retained
directory and verifies the credentials at `POST /api/login`.

After readiness, startup output includes the selected email, password and reminder;
JSON exposes them in `startup_details.details`. Selection authorizes disclosure
without a reveal flag, so treat captured output as sensitive. See the
[startup-details contract](../../docs/manifest.md#components-and-scenes).

`seed` is fresh-only and initializes `account`: stop/run and restart retain the
account/password; reset replaces both. The each-start `test` job checks every
disposable invocation. Applications own seeding/authentication and choose dependency
gates and job policies; application-side edits can invalidate displayed credentials.

## Run daily

From `examples/macos-quickstart` (or its corresponding directory in a Git worktree):

```sh
backlot run dev --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
```

Open the returned `instance.execution.origin`, for example
`https://bl-<instance-label>.backlot.localhost:8443`. The page displays “Native SSR
through the canonical origin: hello from API”; its API link, redirect/cookie link
and WebSocket button use that same origin. The API's listener path `/value` is
published at `/api/value`; `/apix` does not match the API route. Native SSR makes a
verified HTTPS request to `/api/value`, rather than using the API's internal port.
Closing the command leaves the persistent scene running. Record `instance.id`.

```sh
backlot stop --state-dir "$HOME/.backlot" --json INSTANCE_ID
```

Repeat `run dev` to start again with the same logical origin, or use
`backlot restart INSTANCE_ID --state-dir "$HOME/.backlot" --json` for an explicit
application restart. This example has no source watcher; restart after editing it.

## Project setup quickstart

Copy the [manifest](backlot.yaml) and application files for a working starting
point, or adapt your existing commands using this minimal single-service manifest.
The application must listen on `127.0.0.1:$PORT` and serve `/` without depending on
publication during its listener probe:

```yaml
version: backlot/v1
project: my-app
tools: {node: node}
resources:
  http: {kind: port}
  origin: {kind: origin}
components:
  web:
    kind: service
    runtime: native
    command: {tool: node, args: [server.mjs]}
    resources: [http, origin]
    ports: {http: {resource: http}}
    environment:
      assign:
        PORT: {ref: {kind: resource, name: http, field: port}}
        APP_ORIGIN: {ref: {kind: resource, name: origin, field: url}}
        NODE_EXTRA_CA_CERTS: {ref: {kind: resource, name: origin, field: ca_cert_path}}
    readiness: {kind: tcp, target: {ref: {kind: service, name: web, field: port, port: http}}, timeout: 10s}
scenes:
  dev:
    lifetime: persistent
    components: [web]
    resources: [http, origin]
    publish:
      resource: origin
      routes: [{path: /, service: web, port: http, prefix: preserve}]
      probe: {kind: http, target: {ref: {kind: resource, name: origin, field: url}}, timeout: 10s}
```

For a disposable smoke server, save this as `server.mjs` alongside the manifest:

```js
import http from 'node:http';
http.createServer((request, response) => response.end('hello from Backlot'))
  .listen(Number(process.env.PORT), '127.0.0.1');
```

Then run the same `backlot run dev` command above. Replace the server command with
your real application when ready. Applications explicitly map the standard public
CA reference into `NODE_EXTRA_CA_CERTS`; Node reads it at process startup. An
explicit client TLS `ca` option replaces default/extra roots, so libraries using
`ca` must combine roots deliberately. The [application-trust guide](../../docs/https.md#explicit-application-trust)
shows a client-specific bootstrap. Backlot does not detect runtimes or inject trust.
The [manifest guide](../../docs/manifest.md) explains dependencies and environment precedence.

## Tests and two worktrees

The included disposable test verifies SSR, path routing, redirects, secure cookie
round trips and different text/binary WebSocket payloads with normal DNS/TLS:

```sh
backlot run test --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
backlot logs --state-dir "$HOME/.backlot" --component test TEST_INSTANCE_ID
backlot status --state-dir "$HOME/.backlot" --json TEST_INSTANCE_ID
```

The result includes `execution.terminal_exit_code` and a retained JSON report in
`execution.artifacts[].path` (open `result.json` inside that directory). Backlot copies the report before removing disposable
runtime. `backlot run test ... -- --fail` requests a deliberate exit 1 and still
collects a failing report. Add `--keep-on-failure` before `--` only when you want to
retain failed runtime for investigation; later destroy it by recorded ID.

To demonstrate checkout isolation, run from the repository root:

```sh
git worktree add --detach ../backlot-peer HEAD
(cd ../backlot-peer/examples/macos-quickstart && npm ci --ignore-scripts)
backlot run dev --project examples/macos-quickstart --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
backlot run dev --project ../backlot-peer/examples/macos-quickstart --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
```

Record both IDs, origins and `execution.ports`: they must differ. Open both origins;
stopping/destroying one must leave the other working. Stop both published scenes
before `backlot gateway restart --state-dir "$HOME/.backlot" --json`; the gateway
refuses while a published scene is active. Compare `shasum -a 256` of the public
`root.crt` before/after gateway restart, then run each scene again: the CA and each
logical origin persist. Backlot selects fresh available service ports as needed.

## Recovery and cleanup

| Symptom | Action |
| --- | --- |
| Certificate error / `publication_unreachable` | Verify normal hostname resolution and trust in each consumer. Install the owned CA explicitly, refresh daemon trust as above, then restart the failed instance by ID. `NODE_EXTRA_CA_CERTS` only trusts Node applications. |
| Gateway port conflict | Inspect the listener with `lsof`; choose free explicit ports before first start. Never kill the unrelated owner. Retained gateway configuration must match; see [HTTPS recovery](../../docs/https.md). |
| Managed gateway unavailable | `backlot gateway status --state-dir "$HOME/.backlot" --json`, then explicit `gateway start` with your private config. Inspect the protected gateway log; restore access before retrying scene cleanup/restart. |
| Node unavailable | Configure an absolute `tools.node`; the daemon cannot inherit later shell/version-manager changes. |
| Test failed | Read retained logs/report. Destroy kept runtime explicitly; do not treat collected evidence as test success. |

Clean up only your recorded IDs:

```sh
backlot destroy --state-dir "$HOME/.backlot" --json INSTANCE_ID
backlot destroy --state-dir "$HOME/.backlot" --json PEER_INSTANCE_ID
backlot destroy --state-dir "$HOME/.backlot" --json TEST_INSTANCE_ID
# From the repository root, after all peer work has stopped:
git worktree remove ../backlot-peer
```

Evidence remains under the [retention policy](../../docs/evidence.md). Stop the
gateway/daemon only when **all their users** are done; preserve state for CA reuse.
Do not delete state, revoke shared trust, stop unrelated gateways or prune resources
by name. This guide covers macOS ARM64 host/browser/native HTTPS only; container
HTTPS, Linux/WSL and production release acceptance remain separate work.
