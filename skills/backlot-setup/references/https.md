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

Start the daemon and configured gateway with `infra start`, then authorize host
trust separately:

```sh
backlot infra start --json
backlot gateway trust status --json
backlot gateway trust install --fingerprint ROOT_SHA256_FROM_STATUS --json
```

Gateway start (including through `infra start`) reports the actual pinned root
fingerprint, an informational host trust observation and the exact state-scoped install command. Missing or unknown
trust does not fail an otherwise valid gateway start or offer an interactive
installation. Published-scene readiness still requires normally verified HTTPS.

### Explicit host trust and retirement

`gateway trust status` validates a read-only public root snapshot against the
selected daemon's protected managed configuration, CA key pairs and DER SHA-256
pin. It reports exact-root OS trust (`trusted`, `untrusted` or `unknown`) separately
from a fresh daemon-effective observation under its retained environment.
Unavailable observations mean unknown; certificate presence, tool success or TLS
through another root is insufficient. The snapshot excludes keys and private
capabilities. External gateway trust remains operator-owned.

On macOS, host `trusted` means **configured OS trust**: exact supported admin TLS
root settings, exact System public entry and current CA dates. It does not prove
source-account acceptance, override user deny/other stores or establish application
TLS readiness. Native Go SSL policy may leave daemon-effective standalone CA trust
`unknown` after installation; verify the actual application origin normally.
File overrides follow Go policy and alone prove neither verifier selection nor
acceptance. Ubuntu/Debian use fresh exact-root Go verification.

`install` requires the displayed fingerprint and foreground stock-tool
administrator authorization: macOS `security` targets the System keychain/admin
TLS domain; Ubuntu/Debian use
`/usr/local/share/ca-certificates/backlot-FINGERPRINT.crt` and
`update-ca-certificates`, including distribution hooks that can affect more than
one root. Retirement uses stock OpenSSL to refresh indices after removing owned
links; preserved external/legacy links may retain dangling references for manual
review. Backlot preserves pre-existing identical entries/settings, rejects
conflicts and verifies exact postconditions. Ambiguous/unsupported macOS settings
require manual handling. The unprivileged daemon sets Caddy `install_trust:false`.

Permissioned version-1 public receipts live in
`~/Library/Application Support/backlot/trust` on macOS or
`${XDG_CONFIG_HOME:-$HOME/.config}/backlot/trust` on Linux. Only foreground trust
commands write them, atomically under a cooperative mutation lock. They retain
the validated snapshot, exact store/effects, pre-existing versus new effects and
known sources, without keys, and survive daemon erasure. Linux receipts bind new
source filesystem identity/change time and exact generated `.pem` links;
identical replacement does not inherit removal authority. External/legacy links
stay preserved. Status lists bounded retained-root summaries: source observations
are incomplete consumer information, not enrollment. Status/startup neither take
the mutation lock nor depend on healthy receipts.

To replace a root, first install the new selected daemon's root and verify its OS
trust, then select the old receipt fingerprint:

```sh
backlot gateway trust retire --fingerprint OLD_ROOT_SHA256 \
  --replacement --state-dir /absolute/new-daemon-state --acknowledge-impact --json
```

`--replacement` revalidates the selected new root and proves its OS trust before
retirement. Omit it for standalone removal; both require `--acknowledge-impact`.
Review known sources and accept possible breakage of unknown consumers.
Acknowledgement never waives ownership: only verifiably Backlot-provisioned CAs
**and exact Backlot-provisioned trust effects** can be retired. Names, arbitrary
paths and manual imports grant no authority; preserve external entries/settings
even for the same root. Retained public identity permits retirement after source
daemon/key loss or certificate expiry. `status --fingerprint OLD_ROOT_SHA256`
observes a retained root without its daemon. Insufficient legacy/manual receipts
require manual OS work.

On macOS, use an interactive login session: Security may require administrator
authentication even under `sudo`. Retirement warns on stderr while JSON stdout
remains one result. Headless completion is unproven. Waiting does not establish
success/failure; interruption can leave uncertain effects, and Ctrl-C does not
prove all privileged descendants terminated.

Interrupted/denied operations retain evidence and report partial/unknown effects;
do not blindly retry. Changed targets/settings stop further destructive work.
Restore compatible tracking or use
`install --new-tracking --fingerprint ROOT_SHA256_FROM_STATUS` to preserve old
receipts and track only newly validated future effects, never adopt old unknown
effects. Unsupported writers preserve evidence even with `--new-tracking`; use a
compatible executable or separate clean tracking location. Known receipt backup
restoration can leave stale authority: use manual recovery or clean tracking.
The cooperative lock excludes no unrelated administrator; use a maintenance
window. Exact pre/post checks cannot make effects atomic against external changes.

Retirement reports trust withdrawal, entry deletion/preservation and current root
trust separately. macOS withdraws only proven admin TLS settings and preserves
the public entry for review: stock observations cannot prove stable entry identity
after an identical external re-import. Other stores/domains or explicit application
roots may retain trust. Configure browser/application trust separately and verify
the actual origin without warnings. Backlot does not automate browser stores,
DNS, CA regeneration or privileged services.

#### Manual macOS verification and recovery

Use an explicitly authorized disposable macOS host with an interactive administrator
session and a maintenance window. The hosted-only fixture is not an interactive
manual execution path; do not spoof its runner grant. Record the revision, tool
versions, exact DER SHA-256 fingerprint, selected admin domain/System keychain,
pre-existing settings/entry and retained receipt before effects.

1. Start a private managed gateway, inspect `gateway trust status --json`, then
   install only its pinned root using the foreground command above. Inspect status
   again from the same live daemon. Configured host trust and daemon-effective
   observations are separate; neither proves application-leaf TLS.
2. Check wrong-pin installation and retirement without `--acknowledge-impact`
   refuse effects. In a controlled maintenance window, retire the receipt-pinned
   root with acknowledgement and complete normal Security authentication. Verify
   exact admin TLS settings are absent, the System public entry is preserved and
   the receipt reports the observed outcome. Check external pre-existing effects
   remain unchanged. Record failures, unknowns and residuals as such.
3. If denied, interrupted or still waiting, retain the receipt and public snapshot
   unchanged. Determine whether the original privileged operation is still active
   before any further effects; do not infer descendant completion from Ctrl-C.
   Compare the retained certificate's DER SHA-256 with the pinned fingerprint and
   inspect `security dump-trust-settings -d` and the exact System keychain entry.
   A matching name, a missing public entry or an `untrusted` observation alone does
   not prove admin settings were withdrawn.
4. Only after separately authorizing manual recovery and establishing that the
   exact settings were Backlot-created and remain unchanged, an administrator can
   run `sudo security remove-trusted-cert -d /absolute/verified-public-root.pem`
   interactively. The file must contain the verified retained public certificate,
   never a name-selected replacement. Reinspect the exact settings and public
   entry afterward; stock exit success alone is insufficient. Preserve external
   or ambiguous effects, including identical re-imports. Keep the public entry
   for manual review; never delete by name or erase/rewrite the receipt to regain
   automated authority. Record residuals and preserve evidence if uncertain.

This is an acceptance procedure, not evidence of a pass. Optional normal
application-leaf TLS proof remains separate.

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

`infra status` combines compatible daemon access with gateway readiness and bounded
recorded blocker guidance. `infra stop` cleans up the selected scope, then verifies
state ownership release; it preserves CA identity. `gateway stop` stays gateway-only
and reports activity/infra-stop guidance when published workloads block it.

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
