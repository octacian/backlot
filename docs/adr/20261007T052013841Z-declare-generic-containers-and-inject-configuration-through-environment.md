---
title: "Declare generic containers and inject configuration through environment"
date: 2026-10-07
status: superseded
implementation: implemented
summary: "Use generic native/container components and explicit environment mappings to avoid database-specific provisioning in V1."
tags: [configuration, containers, networking]
supersedes: []
superseded_by: [/docs/adr/20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md]
related: []
tracking: []
implemented_by: ["https://github.com/octacian/backlot/commit/699bf468e92c67e711a0b9c6c47409003c91163f"]
---

# Declare generic containers and inject configuration through environment

## Context and Problem Statement

Development needs native watchers while tests and previews may use containers.
Private database images already supply initialization; a database-specific resource
would duplicate that behavior before shared servers are needed.

## Considered Options

- Generic components with explicit environment mappings.
- Specialized SQL resources: add grants and reclamation responsibilities.
- Compose import or generated application configuration: add another configuration
  boundary and replacement/staleness risks.

## Decision Outcome

Declare reusable native or generic container services and jobs. Inject instance
values through explicit environment mappings and read-only literal seed files.
Backlot owns networks, storage, ports, secrets and lifecycle; images/applications
own database initialization, users, migrations and fixtures. Preserve secrets with
retained storage. Never rewrite application configuration.

Originally, published scenes used local Docker and externally configured Caddy,
with bounded path routing and distinct HTTPS hostnames under an operator-managed
domain. DNS and certificates were external setup, and Caddy was required only for
publication. Gateway mutation was limited to the owned scope.

The replacement changes the external-only HTTPS choice; generic components and
explicit application configuration remain in force.

### Consequences

Different languages and images work without runtime-specific adapters. Projects
must expose environment inputs, probes and credential mappings; existing Compose
configuration may be duplicated. Shared-server support can justify specialized SQL
allocation without making private databases special resources.

## More Information

[Manifest](/docs/manifest.md), [container contract](/docs/containers.md).
