---
title: "Declare generic containers and inject configuration through environment"
date: 2026-10-07
status: accepted
summary: "Use generic native/container components and explicit environment mappings to avoid database-specific provisioning in V1."
tags: [configuration, containers, networking]
supersedes: []
superseded_by: []
---

# Declare generic containers and inject configuration through environment

Declare reusable native or generic container services and jobs. Inject instance values through explicit environment mappings and read-only KEY=value seeds. Private databases are ordinary containers; Backlot does not generate application configuration.

## Context

Development needs native watchers while previews/tests may use containers. Private database images already provide initialization. Specialized SQL allocation is unnecessary until shared servers are supported.

## Decision

Own per-instance networks, storage, ports, secrets, and lifecycle. Images/applications own database initialization, restricted-user setup, migrations, and fixtures. Preserve secrets with retained storage. Backlot does not write/remove application config files.

Use a local Docker engine and configured local Caddy gateway. Published scenes declare bounded path routing with distinct HTTPS hostnames under an operator-managed domain. DNS/certificates are external setup; Caddy is required only for published scenes. Mutate only the owned gateway scope. Defer Compose and raw Caddyfile import.

## Consequences

Different languages and database images work with fewer special-purpose adapters. Projects must expose environment inputs, probes, and credential mappings. Some existing Compose configuration may be duplicated.

## Alternatives considered

A MariaDB resource duplicates private image initialization. Shared servers add grants/reclamation responsibilities. Compose import adds a configuration boundary before adoption benefits are proven. Generated config files risk replacement or stale dynamic values.

## Revisit when

Revisit database resources for managed shared servers and Compose/CI reuse when adopting projects demonstrate reduced scaffolding.

## References

- [Configuration contract](../prd.md#manifest-and-configuration)
- [Networking contract](../prd.md#networking-and-https)
