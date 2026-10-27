---
title: 'Separate declaration discovery from bounded recorded inspection'
date: '2026-10-27'
status: accepted
summary: 'Separate prerequisite-free declaration discovery from operational planning and add opt-in bounded recorded views without changing existing wire contracts.'
tags: [cli, discovery, configuration, evidence, compatibility]
supersedes: []
superseded_by: []
---

# Separate declaration discovery from bounded recorded inspection

Expose validated scene declarations before credentials, application tools, providers
or a daemon are available. Add bounded recorded-instance and retained-log queries,
safe configuration provenance and installed contract/schema discovery. Preserve
existing lifecycle authority, strict compatibility and output contracts.

Accepted by the coordinator after independent ADR review on 2026-10-27.

## Context

[Issue 57](https://github.com/octacian/backlot/issues/57) concerns agents entering
unfamiliar projects and finding their recorded work without unbounded output.
Recorded targeting, sampled observation, private setup, current tool diagnostics,
build identity and truthful execution results already exist. Operational planning
still requires effective inputs, seed files and tools; it cannot supply initial
declaration discovery. Existing list and log interfaces lack sufficient filters,
and ordinary log NDJSON has no structured page metadata.

## Decision

Treat declarations, effective configuration, operational plans and recorded runtime
observations as distinct facts. Declaration views validate the complete manifest
with the existing strict decoder and graph validator. They report selected work,
dependencies, lifetime, terminal job, publication and named prerequisites without
reading seeds, private values or machine configuration, resolving executables or
contacting providers. They never imply prerequisites are satisfied or work is ready.
Do not expose environment values, command arguments or sensitive literal values.

Configuration inspection uses the existing layer resolver and defaults. Disclose
only explicit safe settings, source paths and input presence; distinguish caller
selection from recorded selection and current daemon observations. Active daemon
policy comes from the running daemon, independently of scene storage declarations
or newly requested startup settings. Inspection neither changes policy nor invents
historical provenance. Existing doctor supplies current daemon tool lookup detail.

Recorded filters use existing recorded project locations and verified checkout
identity; named lifecycle selection retains its nearest-project rule. They do not parse current manifests
or choose a preferred instance among matching records. Preserve unfiltered history,
explicit IDs and broken-manifest cleanup. Each list page has deterministic identity
order, but separate pages are live queries rather than a retained snapshot: earlier
insertions and changes to filter membership may be missed.

Retained-log paging is explicit opt-in and returns one finite result with records,
continuation and structured retention/collection gaps. Bound returned records,
bytes and per-page log scanning, including filtered-out records; an empty page may
still have continuation. Preserve component, attempt and stream identity and the
existing log encoding. Order by retained source and physical capture order, not
wall-clock time or causal application order. Concurrent appends may appear on a
later request; incomplete active tails are not complete records. Continuation is
not durable replay, and expiration, invalidated sources or daemon replacement must
be visible rather than represented as complete empty history. Older attempts lack
a complete historical collection ledger; never infer completeness from no known gap.

Keep existing endpoints, named result shapes and default log NDJSON unchanged.
Use separate read-only contracts for new daemon queries; feature discovery does
not weaken API/state fences or authorize fallback mutations. Unsupported daemon
features fail explicitly. Ship matching manifest, machine and private schemas with
the executable and retain existing build identity. A capability inventory describes
supported contracts, not provider readiness or a second command parser/catalog.

No new persistent state, writer obligations, migrations, indexes, dependencies,
background workers or automatic recovery are warranted. Reuse existing result
views; do not add another execution summary without a concrete remaining need.
Streaming history, general wait/reconnection, tool provenance persistence and
lifecycle changes remain outside this decision.

## Consequences

Users can discover declarations before setup and retrieve relevant bounded output.
New named read contracts and safe projections require compatibility/privacy tests
and documentation; unchanged endpoints remain usable by strict older clients.
Instance scans still scale with retained history. Log cursors do not survive every
source change, and historical collection completeness cannot be reconstructed.

Unused discovery adds no runtime work. Active inspection performs on-demand reads;
existing lifecycle and retention owners retain all mutation responsibility. There
is no new retention or operator cleanup duty. Schema bytes increase binary size.

## Alternatives considered

- Reuse full planning for discovery: smaller surface, but unavailable precisely
  when credentials, seeds, providers or tools have not been configured.
- Extend existing responses or log NDJSON with optional fields/events: fewer
  endpoint names, but strict predecessor consumers reject unfamiliar fields or
  records. Separate opt-in contracts preserve their behavior.
- Keep client-side filtering and full log scans: preserves today's interfaces but
  repeats identity rules and cannot bound relevant inspection output and scanning.
- Retain query snapshots, indexes or a durable event stream: stronger traversal
  and replay guarantees add writers, retention and recovery beyond this need.
- Add a compact result API: existing execution/component outcomes and truthful
  failure results already provide the needed facts; another projection lacks a
  demonstrated consumer after issue 56.

## Revisit when

Recorded scans measurably impede interactive use; an actual consumer requires
stable snapshot traversal, complete historical collection provenance, resumable
replay or a result projection that existing status cannot supply. Reassess those
obligations before adding storage, writers or worker lifetimes.

## References

- [Issue 57](https://github.com/octacian/backlot/issues/57)
- [Shared API](../architecture.md#shared-api)
- [Recorded identity and observation](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Configuration](20261021T001732173Z-register-private-project-configuration.md)
- [Current tool provenance](20261023T211408955Z-explain-native-tool-selection-without-probing-runtimes.md)
- [CLI parsing and results](20261027T183153280Z-use-framework-parsing-and-preserve-truthful-cli-results.md)
- [Installed identity](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md)
- [Evidence contracts](../evidence.md#logs-and-retention)
