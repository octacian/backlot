---
title: "Use Go with a shared typed local API and urfave CLI"
date: 2026-10-07
status: accepted
summary: "Use Go and shared versioned wire types with urfave/cli to minimize deployment dependencies and client/server drift."
tags: [go, api, cli, platform]
supersedes: []
superseded_by: []
---

# Use Go with a shared typed local API and urfave CLI

Ship one Go executable for CLI and daemon, targeting macOS ARM64 initially. Use urfave/cli v3. Define JSON requests, responses, errors, and events once in api/v1; clients and handlers marshal/unmarshal those same named types.

## Context

The daemon needs native process control, Docker/Caddy integration, durable state, and a future UI interface. Keep Linux/native Windows feasible and avoid maintaining duplicate contracts or infrastructure implementations.

## Decision

Use versioned HTTP/JSON over a permissioned per-user Unix socket. Separate OS-specific transport/supervision from lifecycle logic. Validate decoded input and reject incompatible versions; shared types do not replace validation.

Prefer established libraries and standard-library capabilities. Add dependencies when used. Docker/state/daemon implementations follow the scaffold, which contains only the version contract and CLI. Prefer the official Docker Go SDK and a small Caddy HTTP adapter.

## Consequences

Go callers share compile-time field definitions without code generation. No Node runtime is required. Non-Go clients still need documented compatibility. Native process-tree ownership requires platform-specific work.

## Alternatives considered

Cobra has a broad ecosystem but no required Backlot integration currently justifies it over urfave/cli. Both provide the command/flag structure; urfave matches maintainer usage. Independent payload structs/maps permit drift. LAN administrative HTTP is unnecessary.

## Revisit when

Revisit the CLI library for a concrete code-reducing integration; revisit transport for native Windows or UI requirements while preserving contracts and local access.

## References

- [Shared API](../architecture.md#shared-api)
- [Version contract](../../api/v1/version.go)
- [urfave/cli](https://github.com/urfave/cli)
- [Cobra](https://github.com/spf13/cobra)
- [Docker SDK](https://docs.docker.com/reference/api/engine/sdk/)
