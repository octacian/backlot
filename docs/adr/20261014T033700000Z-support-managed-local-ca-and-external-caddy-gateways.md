---
title: 'Support managed local CA and external Caddy gateways'
date: '2026-10-14'
status: accepted
summary: 'Add a dedicated native local-CA gateway while preserving external Caddy and explicit application configuration to make host HTTPS practical without public DNS.'
tags: [gateway, https, trust, lifecycle, configuration, containers]
supersedes: [20261007T052013841Z-declare-generic-containers-and-inject-configuration-through-environment.md]
superseded_by: []
---

# Support managed local CA and external Caddy gateways

Support explicitly selected managed and external Caddy modes. Managed mode runs
an explicitly configured stock native Caddy executable under the daemon's existing
authenticated guardian, with private persistent CA/configuration, loopback-only
listeners and `*.backlot.localhost:8443` origins. Caddy issues and renews its local
wildcard certificate. The daemon never installs trust. Users separately approve
stock `caddy trust` against the owned admin endpoint.

This supersedes the earlier external-only gateway decision. Its generic component
and explicit environment contracts remain: native/container commands and images
own application preparation; Backlot owns instance isolation and never rewrites
application configuration or detects runtimes to inject trust settings. Existing
machine configurations with no mode continue to use external Caddy.

## Context

Requiring a public domain, manual DNS and external certificate provisioning makes
local HTTPS expensive to adopt. The approved scope targets macOS ARM64 native
host/browser consumers. Docker execution remains useful for dependencies and
mapped HTTP upstreams; container consumption of local HTTPS is outside this slice.
The [product issue](https://github.com/octacian/backlot/issues/18) authorizes this
change. A trusted browser and normal hostname resolution must be demonstrated in
an isolated macOS environment before claiming adoption readiness; native fixtures
with explicit private roots do not prove that gate.

## Decision

Keep external gateway configuration and exact scoped route mutation unchanged.
Persist a random private managed scope identity before effects and use it in
bootstrap, saved-state validation and route intents. A replacement listener with
only the conventional scope cannot satisfy publication; root ETags reject a
configuration replacement between observation and mutation. This closes accidental
port-reuse races without signaling the replacement owner. It is not authentication
against hostile same-user code reading the trusted-user loopback admin API.
Managed defaults are admin `http://127.0.0.1:2020`, HTTPS port 8443, scope
`backlot-managed`, domain `backlot.localhost` and upstream address `127.0.0.1`.
Allow explicit unprivileged admin/HTTPS ports, never fallback or take over an
occupied port. Bind HTTPS on IPv4/IPv6 loopback, disable HTTP redirects and HTTP/3
listeners, and disable automatic CA trust installation in the bootstrap.

Gateway start/stop/restart/status are daemon operations. Scene launch requires a
started managed gateway. Serialize gateway lifecycle with scene acceptance, and
refuse stop/restart while any published scene is active or kept. Stopping a scene
removes only its exact journaled route, preserving other routes and all retained
scene data; stop/start retains instance identity and canonical origin.

Use the accepted native guardian protocol: journal launch intent, persist birth
identity and random control capability before activation, and stop only through
authenticated cleanup. On shutdown/recovery attempt scene route cleanup first,
then stop the managed gateway. Recovery interrupts workloads and gateway, preserves
CA/data and requires explicit start/restart. Unavailable Caddy retains exact route
intents and cleanup uncertainty while daemon administration stays available.
Explicit gateway start can resume the saved effective configuration to permit
exact cleanup retries; it does not resume scenes.

Bootstrap is used only on first activation. Later starts validate the private
saved effective configuration against the owned structure, allowing dynamic scope
children, and use Caddy `--resume`. Missing/changed saved structure, unusable CA
keys or changed root identity fail explicitly; never reset CA identity or replay
stale bootstrap. Unknown guardian authority remains untouched and blocks start. Managed gateway
authority requires state format 4; older formats are rejected without modification.

Preserve generic native/container declarations, explicit environment references,
private networks/storage/ports/secrets, image-owned database initialization and
the prohibition on generated application configuration from the superseded ADR.
No new privileged helper, installer, runtime detection, TLS bypass, DNS/resolver
management, public certificate automation or Linux/WSL support guarantee is added.

## Consequences

Developers can select local HTTPS without owning public DNS. Private state must be
retained with the daemon directory and CA identity backed up together with it.
Trust is an explicit operator action and may require browser-specific setup;
gateway process readiness alone cannot attest client trust. Damaged private state
or lost guardian authority requires diagnosis, not automatic recovery by deletion.
Loopback origins are host-only and do not imply container/LAN reachability.

## Alternatives considered

External-only Caddy retains the previous adoption burden and remains an available
mode. A custom trust helper or bundled installer increases privileged scope.
On-demand certificate issuance is unnecessary with an owned wildcard hostname
space. Automatically restoring bootstrap can erase live or uncertain routes.
PID-based cleanup cannot provide the accepted durable ownership guarantees.

## Revisit when

Revisit when container HTTPS consumers, new platforms, different browser trust
mechanisms or adopting applications require a broader supported configuration.

## References

- [HTTPS operation and trust](../https.md)
- [PRD networking](../prd.md#networking-and-https)
- [Native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Caddy local HTTPS](https://caddyserver.com/docs/automatic-https#local-https)
- [Caddy trust and resume](https://caddyserver.com/docs/command-line)
