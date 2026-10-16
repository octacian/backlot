# Published HTTPS and mixed execution

Backlot supports an explicit daemon-managed native local-CA gateway and an
externally managed Caddy JSON API. Omitted `caddy.mode` retains external mode.
Only published scenes require a gateway; other scenes and metadata-only `prepare`
do not contact it. The [gateway decision](adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
records ownership and trust boundaries.

## Managed local HTTPS

For Linux prerequisites, resolver setup and explicit bridge reachability, follow
[platform operations](platforms.md). The following executable path is for macOS.
Install stock native Caddy separately through your package manager; for Homebrew,
`brew install caddy`. Configure its absolute executable path explicitly, such as
`/opt/homebrew/bin/caddy`. Backlot does not install dependencies or custom modules.
Use a private machine file outside the checkout:

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
hostname; see [container trust and mapping](#public-root-file-mounts-for-containers).

Start the daemon and gateway explicitly, then install trust as a separate operator
action using the configured stock executable and **owned managed admin endpoint**:

```sh
backlot daemon start --state-dir /absolute/private/daemon --json
backlot gateway start --state-dir /absolute/private/daemon --config /absolute/private/machine.yaml --json
/opt/homebrew/bin/caddy trust --address 127.0.0.1:2020 --ca local
backlot gateway status --state-dir /absolute/private/daemon --json
```

[Caddy trust](https://caddyserver.com/docs/command-line#caddy-trust) fetches the CA
from that running endpoint and may request administrator authorization. Approve
this separate command according to your machine policy. Do not point it at an
unrelated Caddy server. The daemon runs unprivileged and sets `install_trust:false`;
it never changes system/browser trust. Configure trust explicitly for each consumer; native applications can use the
[public CA reference](#explicit-application-trust), while standard system/browser
trust setup is a separate operator action. Browsers can use different trust stores: verify your
browser accepts the origin without a warning; no universal browser guarantee is
made. Missing/wrong trust fails normal TLS readiness, never invokes a bypass. The daemon
aggregate probe also needs system trust independently of Node/browser settings. If
it attempted HTTPS before trust installation, stop/start the daemon and explicitly
start the gateway again to refresh cached certificate roots; retained CA identity
is preserved.

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
backlot stop --state-dir /absolute/private/daemon --json INSTANCE_ID
backlot gateway stop --state-dir /absolute/private/daemon --json
backlot gateway start --state-dir /absolute/private/daemon --json
backlot gateway restart --state-dir /absolute/private/daemon --json
```

The first start requires `--config`; subsequent starts/restarts can reuse the
retained configuration. A supplied configuration must match retained gateway
settings. Stop/restart refuse while any published scene is active or kept. Stop
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

Shutdown and crash recovery first attempt exact scene-route cleanup, then stop
the managed gateway through authenticated guardian authority. Recovery preserves
CA and retained data, marks interrupted and requires explicit gateway start and
scene restart. If Caddy is unavailable, exact route intents and cleanup uncertainty
remain; the daemon stays accessible. Explicit gateway start resumes saved state
so scene stop/restart can retry exact cleanup. It never resumes application work.
Unknown guardian ownership is preserved and blocks a new gateway start. Never
signal by PID/name or overwrite saved state to hide uncertainty.

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

Select the same origin in the scene and publish it using the [scene contract](#scene-contract).
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
Backlot contributor tooling. The [runnable macOS quickstart](../examples/macos-quickstart/README.md) supplies
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

The [runnable Go/Alpine example](../examples/container-https/README.md) combines a
native frontend/API with container SSR and a disposable HTTPS client. The same
manifest declarations work for Node containers by explicitly assigning
`NODE_EXTRA_CA_CERTS` instead of `SSL_CERT_FILE`; application code that specifies
its own `ca` must explicitly include the mounted root. No additional Node runtime
matrix is implied by this example.

## External gateway operator setup

For concrete Certbot, file-certificate Caddy JSON, macOS DNS/VPN and host/container
diagnostic steps, follow the [manual macOS setup walkthrough](https-setup.md).

Choose an owned suffix such as `dev.example.com`. Supply wildcard application DNS
pointing `*.dev.example.com` to the host address reachable by host/browser/container
clients. A local router can provide that application DNS; containers and VPN clients
must actually use it. Public DNS-01 challenge records live at the authoritative DNS
provider and are separate from local application A/AAAA records. A publicly trusted
wildcard certificate covers one instance label under the suffix.

Configure TLS externally. DNS-01 automation requires the appropriate Caddy DNS
module and operator-held credentials; standard Caddy does not include provider
modules. Alternatively obtain certificates with an external ACME client using
manual DNS-01 and load certificate/key files into standard Caddy. Manual DNS-01
requires operator renewal before expiry; Backlot does not automate it. Keep keys,
credentials and personal machine configuration outside the checkout.

Install an explicit empty subroute handler in the appropriate HTTPS server's
routes, after any more-specific operator routes. For example, the enclosing route
may match `*.dev.example.com` and delegate to:

```json
{"@id":"backlot","handler":"subroute","routes":[]}
```

The configured ID must identify exactly one subroute handler with an explicit
routes array. Backlot patches only that array's child routes. It does not create
the scope, replace the server, load a Caddyfile or restart Caddy. Parent JSON keys
on the path to the scope must not contain URL path delimiters. Keep the admin API
private and accessible only to authorized local operators/Backlot.

Machine configuration (outside the checkout):

```yaml
version: backlot/v1
docker:
  endpoint: unix:///absolute/local/docker.sock
  host_address: host.docker.internal
  # Optional numeric bind address; omitted means 127.0.0.1.
  # Select a LAN address or 0.0.0.0 only when those consumers require it.
  publish_address: 127.0.0.1
caddy:
  mode: external
  endpoint: http://127.0.0.1:2019
  scope: backlot
  domain_suffix: dev.example.com
  host_address: 127.0.0.1
  # Optional listener port; omitted means 443.
  https_port: 443
```

`caddy.host_address` is the address **as seen by the gateway** for both native
upstreams and mapped container ports. A native gateway commonly uses loopback.
A containerized gateway commonly uses `host.docker.internal`, with container
port bindings explicitly configured on a reachable host interface.
`docker.host_address` is the address **as seen by workload containers** for native
service references; it is required when such references are used. Containers
referencing container services use private instance DNS and internal ports.
Host/native service references use host ports, with the configured mapped bind
address for container targets (loopback for wildcard binds) and loopback for native
targets. Native applications
must explicitly bind their assigned ports to an interface reachable by the chosen
consumers. Backlot never widens native listener interfaces automatically. Numeric
Docker bind addresses retain owned-binding checks and bind-conflict handling.

Host/browser/container clients use the same canonical HTTPS origin. Configure
their DNS and TLS trust independently; loopback in a container addresses that
container. The aggregate probe verifies host DNS, TLS, successful HTTP status (or
same-origin redirect) and the public instance marker set by the proxy route;
container-origin correctness must also be exercised by an application/client job.
Fixture tests trust a private CA only in fixture clients; they do not install trust.

### Gateway restarts and certificate renewal

For an external gateway, the bootstrap JSON is only an initial configuration. Reapplying an old bootstrap
while scenes are live can erase dynamically installed routes. Use Caddy's saved
effective configuration (`caddy run --resume` with the same protected storage and
environment) for ordinary operator gateway restarts. In external mode Backlot does not manage this
process or silently recreate removed live routes.

For file-loaded certificate renewal in this MVP, arrange a gateway maintenance
window: pause all gateway configuration writers, stop every published Backlot
instance by recorded ID, verify its routes are absent, and obtain/replace the
certificate files using the external ACME client. Export the **current effective**
JSON from the private Caddy API to a mode-0600 file, then force-reload that current
configuration so Caddy rereads the certificate files. Do not reload a stale
bootstrap or discard unrelated configuration. Verify HTTPS with normal trust,
restart the recorded Backlot instances, and resume writers. This workflow requires
operator serialization; a root snapshot followed by a whole-config reload does
not provide the concurrency guarantees of Backlot's scoped API mutations.

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
Recovery interrupts execution and requires explicit restart; it does not adopt
foreign routes or continue workloads automatically.

Structured startup categories are available in `execution.failure_detail` and
cleanup categories in `execution.cleanup_details`, alongside readable failure
strings. Gateway diagnostics distinguish scope/configuration, hostname conflict,
concurrency exhaustion, unavailable API and uncertain ownership/effect.

State format is now 4. Older formats are rejected without modification; an older
binary cannot safely interpret managed gateway ownership. See [testing](testing.md)
for isolated fixture commands and separate managed macOS/browser and external
real-domain acceptance gates.
