---
title: "Organize management by daemon checkout and persistent scene"
date: 2026-11-10
status: accepted
implementation: in-progress
summary: "Keep daemon-wide discovery and exact-ID operations while adding checkout discovery and recorded persistent-scene lifecycle wrappers."
tags: [cli, lifecycle, identity, api]
supersedes: []
superseded_by: []
related: [/docs/adr/20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md, /docs/adr/20261027T183153280Z-use-framework-parsing-and-preserve-truthful-cli-results.md, /docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261106T230242771Z-recover-device-only-bindings-after-verified-consumer-settlement.md, /docs/adr/20261106T230626113Z-fence-workload-admission-during-bounded-bulk-stop.md]
tracking: [https://github.com/octacian/backlot/issues/131, https://github.com/octacian/backlot/issues/129]
implemented_by: []
---

# Organize management by daemon checkout and persistent scene

## Context and Problem Statement

Daily inspection requires an explicit checkout path, and restarting, resetting or
destroying a named persistent scene requires manual ID discovery. Changing plain
`list` to local scope would hide other recorded work. Selection convenience must
preserve exact cleanup authority and the existing lifecycle contracts.

## Considered Options

- Three tiers: daemon-wide root commands, checkout discovery, named scene actions.
- Optional-value `--checkout`: shorter, but makes following operands ambiguous and
  complicates the established parser contract.
- Default root commands to the current checkout: hides daemon-wide history and
  changes existing callers.
- Resolve names in callers and invoke ID mutations without revalidation: small,
  but loses the daemon's named-selection fence while waiting for mutation locks.
- Checkout-wide lifecycle commands: require broader destructive intent and target
  coordination than these single-scene operations need.

## Decision Outcome

Keep root `list` daemon-wide by default, including explicit `--checkout PATH`.
Add `checkout list [--checkout PATH]`; omission resolves the enclosing Git checkout
from the caller's canonical working directory, including subdirectories and linked
worktrees. Outside a Git checkout, implicit selection fails with explicit-location
and recorded-ID guidance. Explicit paths retain the existing verified checkout
location contract: `PATH` names the checkout root, including non-Git roots; only
omitted selection searches for an enclosing Git checkout. Paths remain required
flag values; use the existing parser, filtering and pagination rules. Ordinary
recorded filters intersect checkout scope; `--project` cannot replace it on
`checkout list`. Root `list --project PATH` retains exact-project discovery.
Checkout discovery spans
recorded projects within that checkout, rather than selecting its nearest project.

Report the daemon-resolved checkout independently of rows in scoped human and JSON
lists, including empty pages. Make that wire metadata opt-in so existing strict
list consumers retain their response shape. Keep rows, history membership and
continuation semantics unchanged. Reads neither enroll nor repair identities;
unavailable or conflicting locations require explicit recovery or recorded IDs.

Add `scene restart SCENE`, `scene reset SCENE` and `scene destroy SCENE`, with
`--project` selection and the applicable flags of their ID counterparts. Resolve
exactly one non-destroyed recorded persistent instance using existing nearest-project
and current-binding rules, independently of current manifest contents. Missing,
disposable-only or ambiguous selections fail nonzero with actionable guidance and
a bounded sample of candidate IDs when available. Mark truncated samples as
incomplete and give an existing filtered-list command for further discovery;
never choose a newest run or fall back to an outer project. Existing named
status/stop absence behavior remains unchanged.

Carry named selection into the daemon's existing lifecycle serialization and
revalidate the selected ID and checkout before mutation effects. Selection changes
conflict rather than redirecting the operation. Preserve workload admission,
source/association validation, evidence-lock ordering, owner joins and private
resource proofs. Reuse the lifecycle operations and their `InstanceResponse`
output, waiting, cancellation and startup-disclosure behavior rather than adding
scene-specific mutation outcomes. Root exact-ID operations remain available.

Restart retains data and refuses protected drift; reset replaces the generation
under the same logical ID; destroy removes owned data and retained evidence while
leaving caller exports. Neither destructive operation is a rollback transaction.
Keep existing force limits and export-first guidance. Add no consent policy,
implicit start, reset/destroy escalation or checkout-wide lifecycle operation.

### Consequences

The convenience layer crosses CLI, shared wire contracts, client validation and
daemon selection, but introduces no persistence, migration, index, background
owner, timer or dependency. Lifecycle writes and workers remain owned by existing
operations; unused convenience commands add no ongoing work. Recorded discovery
retains its existing scan cost and live-page limitations. Strict older peers may
require an explicit upgrade for new opt-in contracts; never retry a mutation by
silently dropping its named-selection fence. This adds to the related decisions
without replacing their unaffected identity, lifecycle or result guarantees.
