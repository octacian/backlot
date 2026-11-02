---
title: 'Publish daemon-owned portable run reports'
date: '2026-10-18'
status: superseded
implementation: implemented
summary: 'Export a fenced settled attempt through the typed daemon API into an audited no-replacement directory with an independently versioned report.'
tags: [evidence, api, security]
supersedes: []
superseded_by: [/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/41"]
implemented_by: ["https://github.com/octacian/backlot/commit/7b25dc078017ed39acd9865484d72c376cfb45d0"]
---

# Publish daemon-owned portable run reports

## Context and Problem Statement

Evidence must remain useful after private runtime removal and relocation.
Action-side private-record copying duplicates selection/audit rules and can race
active producers or pruning. Reports must not execute untrusted summaries or leak
lifecycle capabilities.

## Considered Options

- One daemon-owned portable directory export through the typed API.
- Action-side copying: couples every caller to private state.
- Links to retained files: fail relocation and pruning.
- Replacement publication or check-then-rename: risks data loss under races.
- A report parser/UI framework: adds an unrelated product boundary.

## Decision Outcome

Export one settled execution attempt with a separately versioned JSON report,
script-free HTML entry and copied evidence. Fence lifecycle mutations and pruning
while selecting/copying. Waiting is explicit and grants no cancellation authority;
kept services remain producers. Strict mode rejects known incomplete evidence;
explicit partial mode records omissions without weakening safety.

Publish outside private state to a new caller-owned destination through private
staging and atomic no-replacement rename. Audit filesystem/key/archive exclusions
before publication. Preserve application report bytes as labelled content and
inventory only copied relative paths, hashes, bounded provenance and original
outcomes. HTML escapes logs and discloses display truncation while retaining originals.

Never serialize private records, plans, machine configuration, secrets or
capabilities. Unknown historical facts remain unknown. Publication failure may
retain an independently settled application result but cannot claim a destination.

The [attempt-history decision](/docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md)
replaces latest-only export selection.
Other privacy, ownership and recovery guarantees remain in force.

### Consequences

Local users, agents and CI share one supported export boundary. Caller storage
owns retention; copying large evidence delays fenced operations. Script-free
presentation reduces summary risk but cannot sanitize arbitrary application
artifacts or replace application secret discipline.

## More Information

[Portable export contract](/docs/evidence.md#portable-per-instance-export).
