---
title: 'Extend managed HTTPS to explicit container consumers'
date: '2026-10-14'
status: superseded
implementation: implemented
summary: 'Extend canonical managed origins to Docker Desktop bridge consumers using explicit Engine host-gateway mappings and read-only public-root mounts without widening gateway listeners.'
tags: [gateway, https, trust, containers, networking, lifecycle]
supersedes: [/docs/adr/20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md]
superseded_by: [/docs/adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/21"]
implemented_by: ["https://github.com/octacian/backlot/commit/d0360059b0b56ebad3ed67c263dbc035e956a10d"]
---

# Extend managed HTTPS to explicit container consumers

## Context and Problem Statement

Containerized SSR and tests need the same canonical HTTPS origins as host clients.
Docker Desktop experiments showed that Engine `host-gateway` reaches the unchanged
host-loopback gateway, with correct-trust success and missing/wrong-trust failure.
That topology evidence does not establish every Engine or browser configuration.

## Considered Options

- Explicit Engine host-gateway mappings and a read-only public-root mount.
- Copied IPs: require refresh after network changes.
- A DNS resolver image: adds provisioning, timeouts and cleanup effects without
  improving the demonstrated topology.
- Broader listeners or implicit trust injection: unnecessarily expand exposure or
  conceal application configuration.

## Decision Outcome

For explicitly attached managed origins, map only the canonical hostname through
Docker's `host-gateway` token at container creation. Engine owns address expansion;
unsupported topology fails without aliases, helper containers or fallback. Keep
native Caddy administration and HTTPS on loopback for this macOS Desktop target.

Mount only the existing pinned public CA file, read-only at an explicit container
path. Reject private-state/ancestor exposure, overlapping mounts and filesystem
aliases before effects. Never mount signing keys or CA directories, copy exported
trust state, relax private permissions, change system trust or disable TLS.
Applications explicitly select their trust settings; Backlot never detects runtimes
or rewrites application configuration.

Preserve scoped route ownership, authenticated supervision, immutable selection
and retained-drift fences. Host-only work remains Docker-independent. External mode
retains operator-owned DNS/TLS and cannot use managed-origin trust contracts.

### Consequences

Host and container consumers share URLs without resolver-helper lifecycle costs.
Running consumers need recreation after Engine network changes; non-root consumers
must already be able to read the mounted file. The demonstrated macOS ARM64 Docker
Desktop boundary does not promise Linux, remote Engines or general LAN access.

## More Information

[HTTPS/trust](/docs/https.md), [Docker host-gateway](https://docs.docker.com/reference/cli/dockerd/#configure-host-gateway-ip).
