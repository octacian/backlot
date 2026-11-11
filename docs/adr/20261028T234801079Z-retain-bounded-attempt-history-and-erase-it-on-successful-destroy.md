---
title: 'Retain bounded attempt history and erase it on successful destroy'
date: '2026-10-28'
status: accepted
implementation: implemented
summary: 'Retain attempt-scoped public outcomes and evidence under bounded daemon retention so replacement preserves diagnosis and successful destroy erases retained history.'
tags: [daemon, lifecycle, evidence, state, api, retention]
supersedes: [/docs/adr/20261018T233600000Z-publish-daemon-owned-portable-run-reports.md, /docs/adr/20261027T215913789Z-separate-declaration-discovery-from-bounded-recorded-inspection.md, /docs/adr/20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md, /docs/adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/99"]
implemented_by: ["https://github.com/octacian/backlot/commit/13d9a3b76ff1b9d06d00f49b8fa3be6ed4059f35"]
---

# Retain bounded attempt history and erase it on successful destroy

## Context and Problem Statement

Replacing one current result loses earlier outcomes and launch provenance.
Aggregate logs cannot safely reconstruct them or support independent erasure.
Diagnosis needs retained public outcomes without archiving private runtime state
or adding event replay. Successful destroy must also erase retained diagnosis.

## Considered Options

- Bounded public attempt records within existing daemon storage and retention.
- Latest-only results or caller exports: lose diagnosis when replacement wins.
- Whole runtime-journal copies: expose secrets and confuse history with authority.
- Event sourcing, SQL or another retention worker: add machinery without a replay need.
- State preflight alone: leaves old destroy callers outside the erasure contract.

## Decision Outcome

Retain a public projection per instance/attempt with transactional acceptance order.
Replacement preserves its predecessor and installs matching current facts atomically;
result writers update only their matching attempt. Keep original terminal outcomes
independent of later cleanup observations. Public history excludes private plans,
configuration, secrets and capabilities; only private runtime journals authorize
cleanup. Application evidence retains its existing sensitivity.

Provide bounded history pages, exact inspection and last-completed selection through
separate typed contracts. Live pages are not snapshots; removal never redirects
selection. Selected logs/export use that attempt's facts, never current-plan or
current-attempt fallback. Missing/pruned facts remain unavailable or unknown.
Settled predecessors may export while newer work runs; active/kept producers remain
ineligible. Preserve export auditing, partial-mode limits and no-replacement publication.

Age/byte expiry removes evidence but retains metadata and explicit expiry facts.
Completed-count and manual pruning erase selected history/evidence; manual age/count
selectors form a union. Recheck protection before effects: active, kept and unresolved
cleanup obligations may exceed caps. Pruning never removes application data or
credentials. Current snapshots remain current truth after completed history is
pruned; later writes cannot resurrect that row.

Persist deletion intent before filesystem effects and retry incomplete erasure under
existing mutation/evidence fences. No database transaction spans file effects;
legacy rewrites preserve unselected records and invalidate affected cursors.
Expire legacy bytes from persisted completion/observation clocks, never file mtime
or restart-renewed age. Preserve protected evidence and unknown bytes during
unresolved cleanup without inventing results or completion times.

Successful explicit destroy joins producers, completes owned runtime/data cleanup,
and erases retained attempt records, evidence and private original-result copies,
including resolved abandonment audits. Leave only an execution-free identity and
targeting tombstone; caller exports survive. Failed cleanup retains diagnosis and
failed evidence erasure remains failure with durable retry intent. Reads never erase.

Require the fixed destroy erasure-contract marker before admission or effects,
including force/retries. Matching clients understand execution-free tombstones;
older clients must upgrade and older strict daemons reject the unknown field.
No marker-free fallback or automatic retry follows an unknown mutation reply.

Use the cumulative database-only v11-to-v12 migration, grounded in genuine closed
predecessor-produced state. Seed only known current outcomes/provenance; overwritten
history cannot be reconstructed. Files adapt after open, outside migration transactions.
This replaces only latest-only reports, the no-history inspection limit and history
retention after destroy; unaffected publication, privacy, image-consent, recovery and
gateway-waiver contracts continue. Start eligibility is a separate decision.

### Consequences

Every accepted execution incurs history writes, even without readers. Existing
retention sweeps retry erasure without a new worker, timer or dependency. Protected
state can exceed retention limits. Destroy irreversibly removes diagnosis: export
first. Native cleanup confidence and unseen-descendant limits remain unchanged.

## More Information

[History and export](/docs/evidence.md#retained-attempt-history),
[retention policy](/docs/evidence.md#logs-and-retention),
[destruction](/docs/evidence.md#investigation-and-destruction).
