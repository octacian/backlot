---
title: 'Support explicit Linux managed HTTPS reachability'
date: '2026-10-16'
status: superseded
implementation: implemented
summary: 'Extend managed HTTPS to bounded rootful Linux bridges with an explicit IPv4 HTTPS bind setting while retaining loopback defaults and private administration.'
tags: [linux, gateway, https, trust, networking, platforms]
supersedes: [/docs/adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md]
superseded_by: [/docs/adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/23"]
implemented_by: ["https://github.com/octacian/backlot/commit/c8074b7f3cd3981825aa57157651bc390a428043"]
---

# Support explicit Linux managed HTTPS reachability

## Context and Problem Statement

Rootful Linux bridges cannot assume Docker Desktop's forwarding to host loopback.
An owned Debian experiment established correct-trust HTTPS through an explicitly
wider IPv4 bind, while loopback-only refused the bridge connection.

## Considered Options

- Explicit operator-selected HTTPS reachability, retaining loopback defaults.
- Automatic listener widening: changes exposure without operator choice.
- Host networking: weakens instance isolation.
- New hostnames, TLS bypass or exported CA directories: conceal compatibility
  errors or widen trust-state exposure.

## Decision Outcome

Support bounded Ubuntu/Debian amd64 local rootful Docker bridge consumers. Accept an
optional canonical IPv4 HTTPS bind setting; omission preserves IPv4/IPv6 loopback.
A specific address adds reachability; explicit `0.0.0.0` widens IPv4 only. IPv6 and
administration remain loopback. The operator owns interface/firewall selection;
conflicts or retained-config drift fail without takeover or implicit fallback.

Retain explicit per-origin host-gateway mappings, read-only existing public-root
mounts, application-selected trust, scoped routes and authenticated supervision.
Never expose keys/private CA parents or relax private state permissions. Host-only
scenes remain independent of Docker.

Real Linux native/provider/browser evidence is distinct from compilation and macOS
fixtures. WSL2 receives guidance only; native Windows is outside this support
boundary. The successor changes native recovery, preserving these HTTPS guarantees.

### Consequences

Linux bridges can use canonical origins, but explicitly wider HTTPS may expose
services beyond the host. Rootless/remote Engines, IPv6 bridges and other
platforms require their own decision and evidence; private administration never
widens merely to solve reachability.

## More Information

[Platform policy](/docs/platforms.md), [HTTPS operation](/docs/https.md).
