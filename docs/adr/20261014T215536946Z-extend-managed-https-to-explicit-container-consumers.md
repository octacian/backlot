---
title: 'Extend managed HTTPS to explicit container consumers'
date: '2026-10-14'
status: accepted
summary: 'Extend canonical managed origins to Docker Desktop bridge consumers using explicit Engine host-gateway mappings and read-only public-root mounts without widening gateway listeners.'
tags: [gateway, https, trust, containers, networking, lifecycle]
supersedes: [20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md]
superseded_by: []
---

# Extend managed HTTPS to explicit container consumers

Extend managed local HTTPS to explicitly selected Linux bridge containers on
macOS ARM64 Docker Desktop. Projects map their published origin to the explicit
Engine `host-gateway` token and mount only the managed CA's existing public root at
an explicit read-only container file path. Application trust remains an explicit
environment or application setting. Keep native Caddy and its administration on
loopback; do not expand listener exposure to implement this topology.

## Context

The managed gateway decision intentionally bounded the initial implementation to
host consumers. [Issue 21](https://github.com/octacian/backlot/issues/21) extends
that boundary to containerized SSR and test clients using the same canonical
HTTPS hostname, rather than special application URLs or TLS verification bypasses.
Owned fixture evidence on Docker Desktop Linux engine 28.5.1 and native Caddy
2.11.6 demonstrated bridge-container access to IPv4/IPv6-loopback host HTTPS via
the Docker-visible host address. Correct public-root trust succeeded; missing or
unrelated trust failed verification. This is topology evidence, not browser or
system-trust adoption evidence.

The user initially chose Docker-visible named-host resolution over copied IPs.
An owned comparison subsequently proved the built-in Engine `host-gateway` token
reaches the unchanged loopback gateway on the supported topology, with verified
trust success and missing/wrong trust failure. It matched `host.docker.internal`
here, while `gateway.docker.internal` was distinct. The user selected the direct
Engine token to avoid a resolver image dependency and extra lifecycle effects.
That explicit choice supersedes the prior named-DNS resolution plan.

## Decision

Keep the existing managed/external ownership, scoped route mutation, private
administration, authenticated guardian and journaled lifecycle contracts.
Components explicitly attach and select the origin, and the scene publishes it.
Container `host_mappings` name that origin and explicitly set `host: host-gateway`.
Only that publication's canonical hostname enters Docker ExtraHosts; no global
DNS changes, wildcard mappings or peer-origin aliases occur. The sole supported
host value is Docker's documented special token. Reject DNS names, copied numeric
addresses, arbitrary aliases, native declarations and duplicate origin mappings.
Pass the token directly at each container create. Engine owns current address
expansion, including fresh consumers after restart; running consumers need
recreation after Engine network changes. Unsupported Engine create failures are
actionable provider failures, with no implicit fallback or name substitution.
No helper image, resolver container, host lookup or cached address is needed.

Mount source `origin_ca_cert` is exclusive with retained storage and checkout
outputs. It requires `read_only: true`, an absolute file target and an attached,
selected, published managed origin. Reject duplicate and overlapping trust mounts.
Validate the existing pinned managed public-root PEM/private state before workload
allocation and again before preparing the Docker mount. Generic storage/output
bind sources cannot include private gateway state or its ancestors, including
filesystem aliases and case aliases. Checkout outputs cannot expose private
daemon state even before CA initialization; retained data uses explicit resource
mounts. Check before creating output directories and again before Docker create.
Never mount signing keys or CA directories, export copies, relax private host-state permissions, or mutate
system trust. Native `ca_cert_path` references retain the existing host path;
container references resolve to that component's declared public-root target.
Only explicit environment assignments can resolve this reference; applications
may instead explicitly read the mounted file themselves. Never detect runtimes,
inject environment implicitly, rewrite application configuration or weaken TLS.

Provider access follows selection: unselected host-only work does not contact
Docker. Retain mapping and mount declarations in immutable snapshots, reject
unsafe retained-contract drift before stopping consumers, and reuse the existing
Docker create intent/capability lifecycle for mapped workloads. There are no extra
resolver effects to reconcile. External mode keeps operator DNS, TLS and lifecycle
ownership; it cannot use these new managed mapping, public-root reference or mount
contracts. Existing external workloads remain unchanged.

## Consequences

Native and container services and tests can share canonical URLs while selecting
their own trust settings and file paths. Direct Engine token mappings avoid copied
addresses and helper-image upkeep. Docker Desktop loopback reachability remains a
bounded, verified topology assumption, not a claim for every Docker engine.
Non-root image users must be able to read the mounted certificate under the image/host permission
model; Backlot does not relax private CA-state permissions. Go on Alpine is the
representative real consumer; Node uses the same explicit `NODE_EXTRA_CA_CERTS`
mapping, with its usual startup and explicit `ca` override semantics.

Linux-host, Windows/WSL, remote engines, alternative runtimes and general LAN
reachability are outside this bounded support claim. Browser/system trust and
final frozen provider/CI acceptance remain separate from fixture evidence.

## Alternatives considered

A numeric-only mapping avoids a resolver operation but requires operator refresh
when Docker Desktop changes its host address; the user rejected that burden.
A dedicated Alpine resolver image could resolve host.docker.internal each launch,
but adds image provisioning/version maintenance, timeouts, containers and journaled
cleanup effects without improving this verified topology. Host DNS resolution
does not establish Docker-visible addresses. Assuming consumer shells or utilities
couples generic images to runtime conventions.
Implicit trust injection and custom dialing obscure the application contract.
Widening HTTPS or admin listeners increases exposure and is unnecessary for the
observed Docker Desktop topology. Any future necessary exposure requires a new
explicit decision supported by evidence.

## Revisit when

Revisit when Docker Desktop changes Engine-token/loopback reachability, a different
engine or platform becomes a supported target, or non-root consumers need a
different public-certificate delivery contract.

## References

- [Existing managed gateway decision](20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md)
- [HTTPS operation and explicit trust](../https.md)
- [Container ownership and recovery](../containers.md)
- [Manifest contracts](../manifest.md)
- [PRD networking](../prd.md#networking-and-https)
- [Docker add-host token](https://docs.docker.com/reference/cli/docker/container/run/#add-entries-to-container-hosts-file---add-host)
- [Engine host-gateway address semantics](https://docs.docker.com/reference/cli/dockerd/#configure-host-gateway-ip)
