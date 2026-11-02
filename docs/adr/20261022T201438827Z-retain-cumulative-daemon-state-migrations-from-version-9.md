---
title: 'Retain cumulative daemon state migrations from version 9'
date: '2026-10-22'
status: accepted
implementation: implemented
summary: 'Retain small transactional Go migration steps from version 9 so users can skip releases without resetting retained state.'
tags: [daemon, state, migration, compatibility]
supersedes: [/docs/adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/66"]
implemented_by: ["https://github.com/octacian/backlot/commit/d3416d9c74e7e581ce98acc73be3f61320dc0a37"]
---

# Retain cumulative daemon state migrations from version 9

## Context and Problem Statement

Users need to skip releases without resetting retained state or finding each
intermediate binary. The owner selected v9 as the forward baseline; maintaining
preproduction v6/v8 compatibility has no adopter justification. Recreating old
fixtures with current structs cannot establish historical compatibility.

## Considered Options

- Retain minimal Go steps inside the existing bbolt transaction.
- Keep only the latest predecessor: fails skip-release upgrades.
- Commit steps independently: exposes partially upgraded state and weakens rollback.
- Existing migration packages: inspected SQL/reader-based or separately committing
  designs do not preserve caller-owned all-step rollback without replacing their
  central behavior.

## Decision Outcome

Retain each real forward migration from v9 onward beside the state opener. Apply
only explicit ordered source/destination steps in the caller-owned bbolt write
transaction. Fresh state starts current; reject pre-v9, newer, malformed or
unsupported paths unchanged. Do not add speculative version bumps, scripting,
plugins, a second history store or downgrade support.

Validate each source before transforming it, then validate current authority and
commit the final version fence in the same transaction. Preserve unknown/passive
and unrelated bytes. Precommit error/cancellation rolls back the complete chain;
a later startup failure never downgrades committed state. Migration performs no
provider, process, registry or retained-file effects; recovery never auto-resumes
workloads.

Retain genuine closed predecessor-produced fixtures, with pinned producer/build
provenance and regeneration instructions, before changing the producer. Keep
supported steps/fixtures and test the oldest supported path through current reopen.
Current structs and synthetic algorithm tests supplement rather than replace that
evidence. Removing a supported path needs an explicit compatibility decision.

Replace only the predecessor's migration policy; native ownership, confidence,
recovery and inherited configuration/privacy guarantees remain in force. Operators
stop the matching old daemon, wait for ownership release and explicitly start the
new binary on retained state; reset is not an ordinary upgrade step.

### Consequences

Users can skip releases with atomic rollback before commit. Maintainers own
historical decoders and fixtures; large future rewrites may outgrow one transaction
and need a new guarantee/cost decision rather than silent batching. No runtime
migration engine or automatic live handover is introduced.

## More Information

[State operation](/docs/containers.md#state-compatibility),
[migration implementation](/internal/daemon/state_upgrade.go),
[retained v9 provenance](/internal/daemon/testdata/state/v9/README.md).
