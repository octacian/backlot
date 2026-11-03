---
title: 'Support managed local CA and external Caddy gateways'
date: '2026-10-14'
status: superseded
implementation: implemented
summary: 'Add a dedicated native local-CA gateway while preserving external Caddy and explicit application configuration to make host HTTPS practical without public DNS.'
tags: [gateway, https, trust, lifecycle, configuration, containers]
supersedes: [/docs/adr/20261007T052013841Z-declare-generic-containers-and-inject-configuration-through-environment.md]
superseded_by: [/docs/adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/18"]
implemented_by: ["https://github.com/octacian/backlot/commit/4cc51b179c96acee2fd1b52b8abf53fc58ffcfca"]
---

# Support managed local CA and external Caddy gateways

## Context and Problem Statement

External DNS, certificates and Caddy setup make local HTTPS costly to adopt. A
managed gateway should preserve canonical origins and explicit trust without
privileged installation, application rewriting or unsafe process takeover.

## Considered Options

- Keep external-only Caddy: preserves the adoption burden.
- Manage a stock native Caddy with its local CA, retaining external mode.
- Bundle a trust helper or installer: expands privileged scope.
- Replay bootstrap on every start: can erase routes or replace retained CA state.

## Decision Outcome

Support explicitly selected managed and external modes. Managed mode runs configured
stock native Caddy under authenticated daemon supervision, with private persistent
CA/configuration and loopback listeners. Canonical local origins use
`backlot.localhost`; the daemon never installs trust. Operators approve stock Caddy
trust separately. External configurations retain operator DNS/TLS ownership.

Journal a private gateway scope before effects. Mutate only owned routes with
configuration preconditions; a conventional scope name or reused port does not
authenticate a replacement. Fail conflicts without takeover or fallback.
Serialize gateway lifecycle with scenes and refuse gateway stop/restart while
published scenes are active or kept.

First activation bootstraps; later starts validate and resume saved effective
configuration. Preserve CA identity and retained scene data. Missing/damaged state,
unknown guardian authority and unavailable cleanup remain explicit failures,
never reasons to reset trust or replay stale bootstrap. Recovery interrupts scenes
and gateway and requires explicit restart.

This replaces only the external-only gateway choice. Generic components and explicit
application configuration remain; container consumers are addressed by the successor.

### Consequences

Local HTTPS no longer requires public DNS, but operators own trust setup and CA
backup. Gateway readiness does not attest browser trust. Loopback origins initially
serve host consumers only; same-user hostile access is outside this boundary.

## More Information

[HTTPS operation](/docs/https.md), [Caddy local HTTPS](https://caddyserver.com/docs/automatic-https#local-https).
