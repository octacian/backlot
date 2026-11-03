---
title: "Use explicit scene lifetimes and daemon-owned execution"
date: 2026-10-07
status: superseded
implementation: implemented
summary: "Use a local daemon and explicit persistent or disposable scenes to centralize isolation and lifecycle guarantees."
tags: [architecture, lifecycle, isolation]
supersedes: []
superseded_by: [/docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
related: []
tracking: []
implemented_by: ["/internal/daemon/runtime.go", "/internal/daemon/resources.go"]
---

# Use explicit scene lifetimes and daemon-owned execution

## Context and Problem Statement

Worktrees isolate source, but applications still collide through ports, origins,
databases, storage and process lifetimes. Project scripts repeat lifecycle and
interruption logic without a common ownership boundary.

## Considered Options

- Project-specific scripts: retain duplicated coordination and cleanup behavior.
- A CLI-only launcher: cannot provide background management across invocations.
- A local daemon with explicit lifetimes: centralizes ownership and recovery.

## Decision Outcome

Use one local daemon with a CLI client. Projects compose reusable components into
persistent or disposable scenes; the daemon owns allocation, readiness,
cancellation, logs and cleanup.

Key persistent instances by project, checkout/worktree identity and scene. Give
each disposable run a fresh identity. Stop preserves persistent data, reset
replaces mutable resources, and destroy removes owned runtime and data. Distinguish
fresh-only initialization from each-start work.

Require explicit restart. Graceful daemon shutdown interrupts work; crash recovery
stops verifiably owned survivors, preserves uncertainty and never resumes or retries
applications automatically.

The [persistent-start decision](/docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md)
replaces only ordinary-start eligibility and input/attempt selection; other
contracts and existing successors remain in force.

### Consequences

Projects share lifecycle guarantees through one interface, at the cost of durable
ownership journals and reconciliation. Persistent environments do not transparently
survive daemon shutdown. Seamless upgrades or continuous previews would require a
new supervision decision.

## More Information

[Architecture](/docs/architecture.md), [lifecycle operations](/docs/daemon.md#identity-and-lifecycle).
