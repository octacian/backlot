---
title: 'Separate declaration discovery from bounded recorded inspection'
date: '2026-10-27'
status: superseded
implementation: implemented
summary: 'Separate prerequisite-free declaration discovery from operational planning and add opt-in bounded recorded views without changing existing wire contracts.'
tags: [cli, discovery, configuration, evidence, compatibility]
supersedes: []
superseded_by: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/57"]
implemented_by: ["https://github.com/octacian/backlot/commit/c524670fc27ba76d4329ade36966d18fbd40105e"]
---

# Separate declaration discovery from bounded recorded inspection

## Context and Problem Statement

Agents need to discover unfamiliar project declarations before credentials/tools
exist and inspect retained work without unbounded scans/output. Operational plans
require resolved prerequisites, and existing unfiltered list/log contracts cannot
supply those discovery and bounded-history views safely.

## Considered Options

- Distinct prerequisite-free declarations and opt-in bounded recorded queries.
- Full planning for discovery: unavailable precisely before setup.
- Extend old strict responses/NDJSON: risks predecessor compatibility.
- Caller filtering/full scans: duplicate identity rules without bounded work.
- Snapshot indexes/event streams: add writers and retention beyond the need.

## Decision Outcome

Keep declarations, effective configuration, operational plans and recorded
observations distinct. Validate declarations/graphs without reading seeds/private
values/machine config, resolving tools or contacting providers. Show named
prerequisites, never values/arguments or a claim that work is ready.

Configuration inspection reveals only safe settings, source paths and input
presence, distinguishing caller, recorded and current daemon facts. Never invent
historical provenance or change policy. Recorded filters reuse verified checkout
and project locations independently of current manifests, preserving explicit IDs,
unfiltered history and ambiguity rules.

Provide explicit finite log pages bounded by records, bytes and scanning, including
filtered-out records. Preserve source/capture order and identity, expose continuation
and collection/retention gaps, and allow empty pages with continuation. Live queries
are neither retained snapshots nor causal ordering/replay; source loss and unknown
historical completeness must remain visible.

Keep existing default responses and log modes unchanged; use separate read-only
contracts for new queries. Unsupported features fail without weakening API/state
fences or falling back to mutations. Ship matching schemas/capability inventory
with build identity; discovery never attests provider readiness.

The [attempt-history decision](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)
replaces the absence of retained attempt history.
Other privacy, ownership and recovery guarantees remain in force.

### Consequences

Users gain initial discovery and bounded relevant inspection without new persistent
state, indexes or background owners. Live pages may miss concurrent membership
changes; cursors may expire and historical completeness cannot be reconstructed.
Measured scan cost or an actual replay/snapshot consumer can justify stronger
storage guarantees later.

## More Information

[CLI discovery](/docs/cli.md), [evidence contract](/docs/evidence.md#logs-and-retention).
