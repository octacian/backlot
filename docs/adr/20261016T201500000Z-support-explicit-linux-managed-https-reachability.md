---
title: 'Support explicit Linux managed HTTPS reachability'
date: '2026-10-16'
status: superseded
summary: 'Extend managed HTTPS to bounded rootful Linux bridges with an explicit IPv4 HTTPS bind setting while retaining loopback defaults and private administration.'
tags: [linux, gateway, https, trust, networking, platforms]
supersedes: [20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md]
superseded_by: [20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
---

# Support explicit Linux managed HTTPS reachability

Target Ubuntu 24.04 amd64 and Debian 13.4 amd64 with a local rootful Docker Engine
and IPv4 bridge consumers. Add an explicitly configured managed
`caddy.https_bind_address` only for HTTPS reachability. Omission keeps the existing
IPv4/IPv6 loopback listeners; administration always stays loopback. WSL2 receives
configuration guidance only, without WSL QA or a support guarantee. Native Windows
execution is not a target.

## Context

[Issue 23](https://github.com/octacian/backlot/issues/23) expands the platform
policy after the [container decision](20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md).
An owned Debian 13.4/Engine 29.3.1 bridge experiment with stock Caddy 2.11.6 showed
connection refusal for a loopback HTTPS bind, then verified correct public-root
trust success and missing/wrong trust failure with an explicit IPv4 wildcard bind.
This experiment discriminates reachability; real CLI/daemon/native/browser and
canonical-origin acceptance remain separate revision-bound gates. Docker Desktop's
observed host-loopback forwarding is not a rootful Linux assumption.

## Decision

Retain the superseded decision's explicit per-origin Engine `host-gateway` mapping,
read-only existing public-root file mount, explicit application trust, scoped route
ownership, authenticated guardian, conservative retained drift and recovery rules.
Never expose private keys/CA directories or relax private state permissions.
Host-only scenes remain independent of Docker. No runtime detection, implicit
trust, custom production dialing, DNS mutation or TLS verification bypass is added.

Managed mode accepts an optional canonical numeric IPv4 HTTPS bind address.
A specific address adds HTTPS reachability while preserving IPv4/IPv6 loopback;
`0.0.0.0` explicitly replaces the IPv4 loopback listener with all IPv4 interfaces.
IPv6 remains loopback. Pin the actual wildcard listener to Caddy TCP4 rather
than generic TCP, which can create a dual-stack IPv6 wildcard. Preflight uses the
same network selector and readiness checks exact owned address families/exposure. Hostnames, zones, multicast addresses and external-mode
bind settings are rejected. There is no default widening or automatic Docker
address selection. The operator selects the actual reachable interface and
firewall exposure. Admin remains `http://127.0.0.1` on a distinct unprivileged port.
Bind preflight, guardian listener ownership and saved-config validation apply to
all configured listeners; conflicts or changed/unavailable settings fail without
fallback or takeover. Retained gateway configuration cannot be silently changed.

Linux uses the existing explicit OS adapters and cooperative supervision contract;
change adapters only when runtime evidence requires it. Ubuntu CI runs fresh
installation, native/process/IPC/permission checks and real provider/HTTPS/browser
gates. Debian requires actual-host runtime evidence. Cross-builds are insufficient;
remove native Windows compile-only CI rather than suggesting planned execution.
Keep macOS gates. The canonical suffix remains `backlot.localhost`; ordinary host
resolver setup and consumer behavior are explicit prerequisites, with compatibility
limits documented in [platform operations](../platforms.md).

## Consequences

Linux consumers can share the canonical HTTPS origin through an explicit bridge
boundary. Explicit wider HTTPS can be reachable from other networks, so the chosen
interface/firewall becomes part of operator configuration. Private admin and CA
signing authority remain unchanged. Linux distribution/browser/Docker combinations
outside the bounded targets remain experimental; WSL documentation does not prove
Windows-host browser reachability or trust. Release readiness still requires exact
candidate evidence and published-HEAD CI.

## Alternatives considered

Keeping only loopback fails the observed Linux bridge topology. Automatic widening
would change exposure based on runtime detection. Host networking would remove
instance network isolation and differs from the approved bridge contract. Public-CA
export directories or permission relaxation expose more state than a file mount.
Replacing the canonical hostname or disabling TLS would hide compatibility errors.
Native Windows runtime implementation exceeds this platform slice.

## Revisit when

Revisit for rootless/remote engines, IPv6 bridge targets, changes to Engine
host-gateway behavior, non-root certificate mount needs, or separately authorized
WSL/browser QA. Any broader exposure or trust delivery contract needs new evidence.

## References

- [Platform policy and Linux/WSL operation](../platforms.md)
- [HTTPS operation](../https.md)
- [Architecture](../architecture.md)
- [Native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Epic 17](https://github.com/octacian/backlot/issues/17)
