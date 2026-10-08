# Published HTTPS and mixed execution

Backlot uses an already-running Caddy JSON API. Only published scenes require
Caddy; native/container scenes without `publish` do not contact it. Metadata
`prepare` remains allocation-free. Backlot never provisions DNS, certificates,
system trust, gateway listeners, Caddy modules or gateway processes.

## Operator setup

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

The bootstrap JSON is only an initial configuration. Reapplying an old bootstrap
while scenes are live can erase dynamically installed routes. Use Caddy's saved
effective configuration (`caddy run --resume` with the same protected storage and
environment) for ordinary operator gateway restarts. Backlot does not manage this
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

State format is now 3. Formats 1/2 are rejected without migration or modification;
use the matching older binary to stop/destroy old environments before selecting a
fresh state directory. An older binary cannot safely interpret publication
ownership. See [testing](testing.md) for isolated fixture commands and the separate
required real-domain acceptance gate.
