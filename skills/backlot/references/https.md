<!-- Generated from docs/https.md; run make skills-generate. -->

## Managed local HTTPS

For Linux prerequisites, resolver setup and explicit bridge reachability, follow
[platform operations](platforms.md). The following executable path is for macOS.
Install stock native Caddy separately through your package manager; for Homebrew,
`brew install caddy`. Configure its absolute executable path explicitly, such as
`/opt/homebrew/bin/caddy`. Backlot does not install dependencies or custom modules.
Use the default private machine file outside the checkout:
`~/Library/Application Support/backlot/config.yaml` on macOS or
`${XDG_CONFIG_HOME:-$HOME/.config}/backlot/config.yaml` on Linux. Daemon state
defaults to `backlot/daemon` under the same OS configuration root. `--config` and
`--state-dir` select other paths.

```yaml
version: backlot/v1
caddy:
  mode: managed
  executable: /opt/homebrew/bin/caddy
  # Defaults shown; explicit ports must be distinct and unprivileged.
  endpoint: http://127.0.0.1:2020
  https_port: 8443
```

Managed mode fixes the configuration scope to `backlot-managed` and uses a private
persisted random scope identity for actual effects. It fixes the suffix to `backlot.localhost`
and upstream address to `127.0.0.1`. HTTPS defaults to IPv4/IPv6 loopback, on
port 8443. Only explicit `caddy.https_bind_address` adds numeric IPv4 reachability
(or selects all IPv4 interfaces with `0.0.0.0`); administration remains loopback. HTTP redirect and HTTP/3 listeners are disabled. Different
ports can be configured explicitly; conflicts fail without fallback/takeover.
Docker may supply dependencies or loopback-mapped HTTP upstreams. Explicitly
configured Docker Desktop bridge consumers can use the same canonical HTTPS
hostname; see [container trust and mapping](https.md#public-root-file-mounts-for-containers).

Start the daemon and gateway explicitly, then authorize host trust separately:

```sh
backlot daemon start --json
backlot gateway start --json
backlot gateway trust status --json
backlot gateway trust install --fingerprint ROOT_SHA256_FROM_STATUS --json
```

Gateway start reports the actual pinned root fingerprint, an informational host
trust observation and the exact state-scoped install command. Missing or unknown
trust does not fail an otherwise valid gateway start or offer an interactive
installation. Published-scene readiness still requires normally verified HTTPS.

### Explicit host trust and retirement

`gateway trust status` obtains a read-only public root snapshot validated against
the selected daemon's protected managed configuration, CA key pairs and DER
SHA-256 pin. It reports `trusted`, `untrusted` or `unknown` for that exact root in
the selected OS store, separately from a fresh observation under the daemon's
retained environment. Unavailable observations mean unknown. Neither certificate
presence, stock-tool success nor a successful request through another root is
proof of trust. The public snapshot contains no signing keys or private scope
capabilities. External gateways retain operator-owned trust management.

`install` requires the displayed fingerprint and runs stock OS tools in the
foreground with normal administrator authorization. On macOS it selects the
System keychain and admin TLS trust domain, using `security`; on Ubuntu/Debian it
uses `/usr/local/share/ca-certificates/backlot-FINGERPRINT.crt` and stock
`update-ca-certificates`, including normal distribution hooks. Retirement also
uses stock OpenSSL to refresh derived certificate indices when owned links are
removed. Preserved external/legacy links can retain dangling references for
explicit manual review. These hooks can
have effects beyond one root. Backlot preserves pre-existing identical entries
and settings, rejects conflicts and verifies exact postconditions. Unsupported or
ambiguous macOS policy/settings require manual handling. The daemon remains
unprivileged and configures Caddy with `install_trust:false`.

Permissioned version-1 public receipts live in
`~/Library/Application Support/backlot/trust` on macOS or
`${XDG_CONFIG_HOME:-$HOME/.config}/backlot/trust` on Linux. Only foreground trust
commands write them, using atomic replacement and a narrow cooperative lock.
They retain the validated public snapshot, exact store/effects, pre-existing versus
new effects and useful known source observations. Linux-owned source receipts
also bind filesystem identity and change time for newly created sources and
exact generated `.pem` links; replacing an identical entry does not transfer its
old removal authority. Pre-existing or legacy links stay preserved. They contain no keys and survive
daemon-state erasure. Status lists bounded retained-root summaries; source
observations are incomplete consumer information, not enrollment or a guarantee
that no application still depends on a root. Status and gateway startup do not
acquire the trust mutation lock. Receipt damage does not prevent ordinary starts.

To replace a root, first install the new selected daemon's root and verify its OS
trust, then select the old receipt fingerprint:

```sh
backlot gateway trust retire --fingerprint OLD_ROOT_SHA256 \
  --replacement --state-dir /absolute/new-daemon-state --acknowledge-impact --json
```

`--replacement` revalidates the selected replacement root and proves OS trust
before removing old effects. Standalone removal omits `--replacement` and still
requires `--acknowledge-impact`. Review known sources and accept possible breakage
of unknown scopes, browsers and applications. Acknowledgement never waives
ownership: only verifiably Backlot-provisioned CAs **and exact Backlot-provisioned
trust effects** can be retired. Certificate names, arbitrary paths and manual
imports grant no retirement authority. Pre-existing external entries/settings
remain preserved even for the same root. Retained public identity can authorize
retirement after the source daemon/keys are gone or the old certificate expires.
Use `status --fingerprint OLD_ROOT_SHA256` to observe a retained root without its
daemon. Legacy/manual effects without sufficient receipts require manual OS work.

Interrupted/denied operations report partial or unknown effects and preserve
intent/evidence. Do not blindly retry them. Changed targets/settings stop further
destructive work. Restore compatible tracking or deliberately use
`install --new-tracking --fingerprint ROOT_SHA256_FROM_STATUS` to preserve old
receipts and track only newly validated future effects. This never adopts old
unknown effects. Unsupported writers preserve evidence unchanged, even with `--new-tracking`;
use a compatible executable or a separate clean tracking location. Receipt backup
restoration can make authority stale: use manual recovery or clean tracking when
restoration is known. The lock coordinates Backlot commands, not unrelated
administrators; use a controlled maintenance window. Exact pre/post checks cannot
make OS effects atomic against an external path or policy change.

Retirement distinguishes trust withdrawal, entry deletion/preservation and current
root trust. On macOS it withdraws only proven admin TLS settings and preserves the
public certificate entry for manual review: stock observations cannot prove stable
entry identity after an identical external re-import. Residual trust in another store/domain or an explicit application
root can remain after the selected owned effects are removed. Browser/application
trust is separate: configure it explicitly and verify the actual origin without
warnings. Backlot does not automate browser stores, DNS, CA regeneration or a
privileged service.

Publication readiness, doctor system TLS and HTTPS probes use bounded, joined,
unprivileged invocations of the same executable to refresh Go trust policy.
No daemon restart is needed merely to refresh system roots. `SSL_CERT_FILE` and
`SSL_CERT_DIR` retained by the daemon still apply to its effective TLS; the separate
OS observation removes these overrides. If they disagree, deliberately update
consumer configuration (and restart a daemon to change its retained environment),
then retry the failed scene. No CA injection or TLS bypass is used.

Run a published scene using the existing scene contract below. Its canonical
origin is `https://bl-<instance-label>.backlot.localhost:8443`; different worktrees
receive different labels. Host/native clients and browsers must resolve the
hostname normally to loopback. No resolver files, `--resolve`, custom dialers or
proceed-anyway browser behavior satisfy adoption acceptance. Owned native fixtures
verify normal host resolution with explicit private client roots; clean-machine
macOS/browser trust and resolution remain a separate acceptance gate until a
suitable disposable environment verifies them. Existing operator resolver setup
can influence local observations.

Gateway status reports owned process/listener readiness, independently from
client trust or scene readiness. Startup errors distinguish configuration,
listener conflict, unavailable gateway, unusable CA/saved state and uncertain
ownership. Inspect the protected `gateway/gateway.log` within the daemon directory
for stock Caddy startup errors. Caddy manages certificate issuance and renewal.

```sh
backlot stop --json INSTANCE_ID
backlot gateway stop --json
backlot gateway start --json
backlot gateway restart --json
```

The first start or restart loads the default machine file when `--config` is
omitted. A missing default names the checked path and asks you to create it or
supply `--config`; unreadable, invalid and non-managed files fail explicitly.
Once configured, omitted `--config` reuses retained gateway settings without
reading the default, even if it changes or disappears. A supplied configuration
must match retained gateway settings. Operation JSON reports `config_source` as
`explicit`, `default` or `retained` for the current selection; later status reports
`retained`. No historical source is stored, and unconfigured status has no source.
Stop/restart refuse while any published scene is active or kept. Stop
those scenes by ID first. Scene stop/start preserves retained data and logical
origins; gateway stop/start preserves CA identity and effective configuration.
Stopping an individual scene never stops the gateway.

All managed configuration, autosave, logs and CA state live under the private
`gateway/` directory inside daemon state. Keep its CA keys private and retain the
whole directory. Ordinary starts resume validated saved effective configuration,
never replay bootstrap. Missing/corrupt saved state, missing/mismatched CA keys or
a changed root fail explicitly rather than silently resetting trust. Restore the
original private state under a maintenance window; deleting it while journaled
process/route ownership remains is unsafe.

### Managed gateway recovery

Shutdown/crash recovery attempts exact scene-route cleanup before authenticated
gateway cleanup, preserves CA/configuration/data, marks interruption and requires explicit
startup. An unavailable API retains route intents for retry through validated saved
configuration. A trusted prior boot releases only obsolete process obligations;
legacy bootless/missing-effect records and closed listeners cannot prove absence.
See [native ownership and force semantics](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/daemon.md#native-execution).

First retry `gateway stop`. If authority remains unavailable, choose restoration,
a controlled reboot, or `gateway stop --force` followed by ordinary `gateway start`.
**Force accepts unknown survivors that may still run/write**; it does not verify
cleanup or signal observed IDs. Active/kept published scenes, exact routes,
containers, ports, configuration, CA and trust checks remain fences. Keep private
state; force never authorizes data deletion or CA replacement.

Private history and safe warnings survive starts and daemon recovery. API callers
select `claim_revision` from gateway status and retain `expected_claim_revision`
through force-stop retries; replacement claims reject stale requests before cleanup.
There is no force start/restart/reset/destroy. Scene force recovery uses
`stop --force INSTANCE` followed by ordinary restart under the same risk.

## Explicit application trust

A native component can map the published origin's `ca_cert_path` to its own
runtime setting. It resolves to the managed gateway's existing public PEM
`root.crt`, under private daemon state, and stays stable across scene/gateway
restarts. No certificate export copy is created. Signing and private keys are
never application inputs. Keep the daemon's entire CA state private and intact.

For Node, add these assignments to an existing native component (the tool,
command and publication are project-owned):

```yaml
resources: [origin]
environment:
  assign:
    APP_ORIGIN: {ref: {kind: resource, name: origin, field: url}}
    NODE_EXTRA_CA_CERTS: {ref: {kind: resource, name: origin, field: ca_cert_path}}
```

Select the same origin in the scene and publish it using the [scene contract](https.md#scene-contract).
The machine configuration must explicitly select `caddy.mode: managed`.
Reusable environment sets can contain these assignments when explicitly included
by native or explicitly mounted container components. External gateways, probe targets and
fixture values cannot use `ca_cert_path`. Planning rejects unsupported use before
allocation; offline plans and metadata preparation retain a symbolic reference
without reading or creating CA state. Runtime requires the started gateway and
validates private paths, usable CA state and its pinned root identity before
allocating workload resources.

Backlot applies the usual [environment precedence](manifest.md#values-and-environment).
Component assignments win over included sets, seeds and pass-throughs. Nothing
is injected implicitly: choose which processes receive trust, and how it combines
with existing settings. This application setting does not configure the daemon's
aggregate HTTPS readiness or install browser/system trust; those consumers need
their own explicit trust setup.

[Node reads `NODE_EXTRA_CA_CERTS` at process startup](https://nodejs.org/api/cli.html#node_extra_ca_certsfile).
An explicit TLS `ca` option overrides the default roots, including extra roots;
a library that supplies `ca` must combine trust deliberately. For example, on
Node versions supporting [`tls.getCACertificates`](https://nodejs.org/api/tls.html#tlsgetcacertificatestype)
(including Node 26), an application-owned bootstrap can create a client-specific
agent:

```js
import { readFileSync } from 'node:fs';
import { getCACertificates } from 'node:tls';
import https from 'node:https';

const localCA = readFileSync(process.env.NODE_EXTRA_CA_CERTS, 'utf8');
// Choose the roots this client should trust. Include application-owned roots
// here as needed; supplying only localCA intentionally replaces other roots.
const ca = [...getCACertificates('default'), localCA];
const agent = new https.Agent({ ca });
https.get(new URL('/api/value', process.env.APP_ORIGIN), { agent }, response => {
  response.pipe(process.stdout);
}).on('error', error => {
  console.error(error.message);
  process.exitCode = 1;
});
```

Pass the agent through the application's supported HTTPS client API; other
libraries can use different transport configuration. Backlot neither detects
Node nor rewrites application code/configuration, replaces existing CA settings,
or disables TLS verification. Node is an optional application dependency, not
Backlot contributor tooling. The [runnable macOS quickstart](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/macos-quickstart/README.md) supplies
frontend/API/SSR and disposable tests using this mapping.

### Public-root file mounts for containers

Container components explicitly declare an origin host mapping and typed mount:

```yaml
resources: [origin]
host_mappings:
  - {origin: origin, host: host-gateway}
mounts:
  - {origin_ca_cert: origin, target: /app/trust/backlot-root.pem, read_only: true}
environment:
  assign:
    APP_ORIGIN: {ref: {kind: resource, name: origin, field: url}}
    SSL_CERT_FILE: {ref: {kind: resource, name: origin, field: ca_cert_path}}
```

The origin must be attached, selected and published, with explicit managed Caddy
configuration. `origin_ca_cert` is exclusive with `resource` and `output`; it
mounts only the existing validated public root as a read-only file at the chosen
absolute target. Duplicate origin mounts and overlapping mount targets are
rejected. Generic storage/output bind sources cannot include private gateway
state or its parent directories, including through filesystem aliases. Checkout
output mounts also cannot expose private daemon state, even before CA initialization;
owned retained data uses declared resource mounts. Keys and CA
directories are never mount sources. The environment
reference resolves to this container target, while native consumers retain the
host public-root path. A mount can be used without an environment assignment when
the application explicitly reads that file itself. No runtime settings are
injected. Choose image users and filesystem permissions that can read the file;
the daemon does not relax private host CA permissions.

Go clients on Alpine can use `SSL_CERT_FILE` with their normal system-root loading.
Node containers can map `NODE_EXTRA_CA_CERTS` to the same `ca_cert_path` instead;
the startup and explicit `ca` override rules above still apply. Applications own
all other runtime-specific trust settings. Container hostname reachability must
also be configured explicitly; mounting a root does not provide DNS.

The bounded topologies are macOS ARM64 Docker Desktop Linux bridges and the
[Linux targets](platforms.md) with a local rootful Engine/IPv4 bridge and stock
native managed Caddy. Linux requires explicitly configured Docker-reachable HTTPS
listeners; the loopback default is unchanged. Configure `host: host-gateway`
explicitly. Backlot passes only the owned canonical hostname and this special
[Engine token](https://docs.docker.com/reference/cli/docker/container/run/#add-entries-to-container-hosts-file---add-host)
to Docker ExtraHosts; Engine chooses the current host address at each container
create. Restart creates a fresh container and refreshes the mapping. Already
running containers need recreation after Engine networking changes. No resolver
image/container, DNS-name substitution, numeric copying, host DNS or fallback is
involved. DNS names such as `host.docker.internal` are not accepted in this field.
An Engine without the required token support fails container creation with a
provider diagnostic. Admin stays on host loopback. HTTPS stays loopback unless the operator explicitly
selects wider IPv4 exposure. Rootless/remote engines, Windows/WSL QA and general
LAN compatibility are outside this support claim. Engine `host-gateway` and Docker Desktop's
`host.docker.internal` matched in owned fixtures; Docker's VM gateway name is a
different address, and equality across other topologies is not assumed.

The [runnable Go/Alpine example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/container-https/README.md) combines a
native frontend/API with container SSR and a disposable HTTPS client. The same
manifest declarations work for Node containers by explicitly assigning
`NODE_EXTRA_CA_CERTS` instead of `SSL_CERT_FILE`; application code that specifies
its own `ca` must explicitly include the mounted root. No additional Node runtime
matrix is implied by this example.

## Scene contract

Declare an `origin` resource, select it in the scene and origin-consuming
components, and map its `url` reference into application environments. A publication
uses selected service ports and one aggregate HTTP probe:

```yaml
publish:
  resource: origin
  routes:
    - {path: /api, service: backend, port: http, prefix: strip}
    - {path: /, service: frontend, port: http, prefix: preserve}
  probe:
    kind: http
    target: {ref: {kind: resource, name: origin, field: url}}
    timeout: 30s
```

Paths are case-sensitive literal segment prefixes: `/api` matches `/api` and
`/api/...`, never `/apix` or `/API`. Longest matching prefix wins; `/` is the
fallback. Segments use ASCII letters/digits and `._~-`, with canonical absolute
paths; wildcard, regex and escaped/ambiguous path declarations are rejected.
`strip` removes the declared prefix (`/api` becomes `/`); `preserve` forwards the
path. Stripping `/` leaves the path intact. Application-generated redirects,
cookies and WebSocket upgrades pass through Caddy; applications remain responsible
for using their canonical origin and compatible base paths.

Service listener probes must become ready independently of publication. Routes
are registered once all routed services are listener-ready, before later dependent
client jobs. The aggregate HTTPS probe runs before a disposable terminal job and
before persistent readiness. Make origin-dependent jobs depend on the routed
services so they cannot run before publication exists. This permits SSR through
the origin without requiring an SSR request to satisfy a service listener probe.

`instance.execution.origin` exposes the canonical origin. Instance labels contain
160 bits of instance identity under one wildcard depth. Stop/start, restart and
reset retain the hostname; destroy followed by run allocates a new logical
instance/hostname. A different listener port changes the origin port while
retaining the hostname. Changing the established domain requires destroy. Gateway
configuration drift requires explicit reset rather than silently redirecting
retained ownership.

## Conflicts, failures and recovery

Before publication, Backlot reads the root Caddy configuration and its ETag,
checks hostname/ownership conflicts, and conditionally patches only the scope's
child routes. A concurrent edit produces a bounded retry against fresh state.
Ancestor wildcard delegation is allowed; foreign hostname claims fail without
takeover. Unrelated configuration and other instances remain intact.

The private journal records the exact ownership-marked route intent before the
external mutation and completion afterward. Stop, failed startup, disposable
completion, shutdown and crash recovery remove only that exact owned effect.
Removal is idempotent even when a mutation response/completion record was lost.
Changed/moved routes or unavailable APIs produce explicit cleanup failure and
preserve uncertain configuration for diagnosis. Fix the provider or inspect the
private journal, then retry stop/destroy; do not prune routes by hostname/name.
Recovery interrupts execution. Explicitly start scenes again after cleanup; recovery
does not adopt foreign routes or continue workloads automatically.

Structured startup categories are available in `execution.failure_detail` and
cleanup categories in `execution.cleanup_details`, alongside readable failure
strings. Gateway diagnostics distinguish scope/configuration, hostname conflict,
concurrency exhaustion, unavailable API and uncertain ownership/effect.

See [state compatibility](containers.md#state-compatibility) for the v9 minimum,
retained saved configuration/CA guarantees and rejection of pre-v9 state without
mutation. [Testing](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/testing.md) distinguishes isolated fixtures from targeted
managed macOS/browser and external real-domain trust checks. Select manual checks
for changed guarantees lacking CI coverage under the [release policy](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/preparing-a-release.md).
