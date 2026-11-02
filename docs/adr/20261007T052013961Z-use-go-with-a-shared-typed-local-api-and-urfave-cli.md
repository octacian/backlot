---
title: "Use Go with a shared typed local API and urfave CLI"
date: 2026-10-07
status: accepted
implementation: implemented
summary: "Use Go and shared versioned wire types with urfave/cli to minimize deployment dependencies and client/server drift."
tags: [go, api, cli, platform]
supersedes: []
superseded_by: []
related: []
tracking: []
implemented_by: ["/api/v1/daemon.go", "/internal/client/client.go", "/internal/cli/command.go"]
---

# Use Go with a shared typed local API and urfave CLI

## Context and Problem Statement

A local daemon needs native process control, Docker/Caddy integration, durable
state and a reusable client boundary. Deployment should not require Node, and
independent client/server payload definitions would drift.

## Considered Options

- Go with shared named wire types and urfave/cli.
- Cobra: capable, but no required integration outweighs maintainer familiarity
  with urfave/cli.
- Independent payload structs or maps: permit contract drift.
- LAN administration: adds an unnecessary network-access boundary.

## Decision Outcome

Ship one Go executable for CLI and daemon. Use urfave/cli v3 and versioned
HTTP/JSON over a permissioned per-user Unix socket. Define requests, responses,
errors and events once in `api/v1`; client and daemon use those same named types.
Validate decoded input and reject incompatible versions: shared types do not
replace trust-boundary validation.

Separate platform transport/supervision from lifecycle logic. Prefer standard
library capabilities and established dependencies when actually used, including
the official Docker Go SDK and a small Caddy HTTP adapter.

### Consequences

Go callers share compile-time contracts without generation, and installation needs
no JavaScript runtime. Non-Go clients still need compatibility documentation;
native supervision requires platform adapters. Revisit CLI or transport choices
only for a concrete requirement that improves the existing boundary.

## More Information

[Shared API](/docs/architecture.md#shared-api), [urfave/cli](https://github.com/urfave/cli).
