---
title: "Use explicit scene lifetimes and daemon-owned execution"
date: 2026-10-07
status: superseded
summary: "Use a local daemon and explicit persistent or disposable scenes to centralize isolation and lifecycle guarantees."
tags: [architecture, lifecycle, isolation]
supersedes: []
superseded_by: [20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
---

# Use explicit scene lifetimes and daemon-owned execution

Backlot runs as one local daemon with a CLI client. Projects compose reusable components into persistent or disposable scenes. The daemon owns resource allocation, readiness, cancellation, logs, and cleanup.

The [persistent-start decision](20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md) partially supersedes this record's ordinary-start eligibility and snapshot-selection rules; unaffected rules
and existing successors remain in force.

## Context

Checkouts isolate source but applications still collide through ports, origins, databases, mutable storage, and process lifetimes. Repeating coordination in project scripts creates inconsistent interruption and ownership behavior.

## Decision

Key persistent instances by project, distinct checkout/worktree identity, and scene. Give each disposable run a fresh identity. Stop preserves persistent data; reset replaces mutable resources; destroy removes owned runtime/data. Record fresh-only initialization separately from each-start jobs.

Use explicit restart without automatic retry or Backlot watching. Graceful daemon shutdown interrupts work. Crash recovery stops verifiably owned survivors, preserves uncertain resources, and requires explicit restart. For the initial scaffold, all four [historical PRD workflows](https://github.com/octacian/backlot/blob/c524670fc27ba76d4329ade36966d18fbd40105e/docs/prd.md#v1-boundary) were release requirements using generic fixtures.

## Consequences

Projects share lifecycle guarantees through one interface. Backlot must maintain durable ownership, interruption handling, evidence, and reconciliation. Persistent environments do not transparently survive daemon shutdown or crashes.

## Alternatives considered

Project-specific scripts retain duplicated lifecycle code. A CLI-only launcher cannot provide the chosen background management contract. Transparent continuation and automatic retries expand V1 supervision complexity.

## Revisit when

A concrete need for seamless daemon upgrades or continuous previews may justify stronger supervision and recovery.

## References

- [V1 PRD](https://github.com/octacian/backlot/blob/c524670fc27ba76d4329ade36966d18fbd40105e/docs/prd.md)
- [Implementation plan](https://github.com/octacian/backlot/blob/c524670fc27ba76d4329ade36966d18fbd40105e/docs/implementation-plan.md)
