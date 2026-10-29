---
title: 'Retain bounded attempt history and erase it on successful destroy'
date: '2026-10-28'
status: accepted
summary: 'Retain attempt-scoped public outcomes and evidence under bounded daemon retention so replacement preserves diagnosis and successful destroy erases retained history.'
tags: [daemon, lifecycle, evidence, state, api, retention]
supersedes: [20261018T233600000Z-publish-daemon-owned-portable-run-reports.md, 20261027T215913789Z-separate-declaration-discovery-from-bounded-recorded-inspection.md, 20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md, 20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
superseded_by: []
---

# Retain bounded attempt history and erase it on successful destroy

Retain each accepted execution attempt independently of its logical instance and
its next attempt. Expose bounded history, last completed results and exact-attempt
inspection, logs and portable artifact export. Reuse bbolt and the existing
retention owner; successful explicit destroy removes this instance's retained
attempt history and evidence, while failed or uncertain destroy preserves diagnosis.

This is the first of two serial changes for [issue 99](https://github.com/octacian/backlot/issues/99).
It does not change start eligibility. Independent ADR review and coordinator
acceptance are complete. This record partially supersedes the portable-report decision's latest-only selection and the recorded
discovery decision's no-history/unknown-historical-ledger limitation. Their safe
publication, bounded query, output and privacy contracts continue by reference.
It also replaces the image-pin decision's ordinary-history retention on destroy
and the inherited boot-recovery rule retaining private scene waiver history even
after successful destroy. Image acceptance/consent, recovery and gateway waiver
history are unaffected. Reciprocal supersession links preserve existing successors and every unaffected
rule of those decisions by reference.

## Context

An instance currently owns one execution result and one private runtime journal.
`acceptExecutionSnapshot` replaces both, and `execution` updates the result during
execution, stop and recovery. Logs already identify attempts, but aggregate files
and instance-wide expiry cannot safely erase one attempt. Artifacts already have
attempt directories. Export selects the latest execution and reads launch
provenance from its current runtime journal. Replacing that journal loses facts
needed to export the predecessor correctly. Destroy currently retains evidence.

The owner approved durable history for every replacement path, exact prior results
even while a replacement runs, manual pruning, bounded automatic retention, and
erasure on successful destroy. This includes restart, reset and stopped/start;
phase two adds eligible failed/cancelled/interrupted start. It does not require a
transition/event journal, general replay, unlimited history or secret history.

## Decision

Keep a private bbolt attempt namespace keyed by instance and accepted attempt,
ordered newest first by a transaction-assigned per-instance sequence rather than
random IDs or wall-clock ordering. Persist an intentional public projection of
execution outcome, known start/completion times, collection/cleanup state and
bounded export provenance. Do not archive whole instances, plans, snapshots,
fixtures, private configuration, secret values or runtime ownership capabilities.
Application logs/artifacts retain their existing application-output sensitivity.
The current instance snapshot remains the current-state interface.

Accepting a replacement atomically preserves the predecessor and installs the new
attempt alongside the existing acceptance transaction. All result writers update
the matching attempt: executor progress/finalization, stop/kept cleanup, lease
cancellation, recovery, native-waiver observations and cleanup-failure reporting.
A stale writer cannot modify another attempt. Preserve original terminal outcomes
separately from later stop/cleanup observations. Existing private journals remain
the sole runtime cleanup authority; public history is never recovery authority.
Completed means execution completion, not successful cleanup or prune eligibility;
kept or unresolved cleanup can therefore be completed and still protected.

Add named typed read requests/responses and thin adapters: `history ID --limit N
--after CURSOR` (default/max 100), `inspect ID --attempt ATTEMPT`, and `inspect ID
--last-completed`. History includes the current accepted attempt and retained
predecessors; last-completed ignores any newer incomplete attempt. No completed
attempt is a typed absence, never the active result. Exact selection never falls
back to current. Pages are live keyset queries with bounded opaque continuations;
concurrent insertions may be missed, deletion must not redirect selection. Missing
or pruned IDs are explicitly unavailable, not successful empty evidence.

Use separate `/v1/attempts/list` and `/v1/attempts/inspect` named contracts so
ordinary inspect responses keep their current shape except for the intentional
execution-free destroyed tombstone described below. Reuse
`logs --page --attempt ATTEMPT ID` and `/v1/logs/page`, making gaps, expiry,
activity and collection knowledge belong to the selected attempt. Preserve default
aggregate logs and NDJSON framing. Extend opt-in export selection with `export ID
--attempt ATTEMPT --output DIR`; omitted selection retains latest-attempt behavior.
Selected export uses that attempt's outcomes, launch provenance, logs and artifacts,
never current-plan facts. A settled old attempt can export while a newer one runs;
active/kept producers remain ineligible even with partial mode. Preserve existing
strict/partial auditing, no-replacement publication and caller-owned destination.
Do not bump the portable report schema merely for selecting a different attempt.

Prune only completed attempts without active producers, kept investigation runtime
or unresolved cleanup obligations. Retain the existing 168h age and daemon-wide
1GiB evidence defaults; add startup `--retention-attempts 100`, a positive completed
attempt count per instance, and expose effective policy through daemon configuration.
Protected work can exceed these bounds; never kill work to satisfy retention.
Manual `history prune ID --older-than D --keep N --dry-run` previews eligible IDs,
reasons/protection and evidence bytes; at least one selector is required, and the
same command without `--dry-run` applies after rechecking eligibility. No persisted
preview, new scheduler, retention service or approval token is required.

The owner confirmed split retention: automatic age/byte expiry removes evidence
bytes and retains metadata with explicit expiration flags; the completed-attempt
count and manual pruning remove selected completed history and evidence. Manual
age/count selectors combine as a union. Preview is advisory, with no approval
token; apply rechecks protection. The existing current instance snapshot remains
current-state truth if count/manual pruning removes its completed history entry,
but exact historical lookup reports that entry unavailable. Successful destroy
also erases current execution details; no historical result remains inspectable.

Serialize pruning/export/destroy with existing evidence and mutation fences using
one consistent lock order; no database transaction spans file copying/removal.
Commit expiry/deletion intent before removing files; retain retryable cleanup
markers until deletion is complete, including after crash. New logs should be
attempt-scoped to avoid rewriting a live aggregate source. Legacy mixed logs must
preserve unselected records under fences; invalidate affected cursors visibly.
Do not claim byte retention is complete until residual files are gone.

Successful destroy joins producers, removes owned runtime/data under existing
fences, then erases all this instance's retained attempt records/logs/artifacts.
Keep a minimal destroyed identity tombstone for recorded targeting, idempotency
and diagnostics of erasure state, with no retained attempt payload, including
original-result copies in private native-abandonment audits once their cleanup
obligations have been resolved. Report failure
if evidence erasure fails and retry it on explicit destroy or existing recovery/
retention machinery. Failed or uncertain runtime/data destroy retains diagnostic
history; never prune its unresolved obligation. Caller-owned exports are untouched.
Pruning never removes application data, credentials, snapshots or shared allocations.
Destroy retains its existing authority for those resources; history grants none.

The destroyed tombstone omits `execution`; never retain or fabricate an attempt to
satisfy an old validator. Preserve identity/location, preparation allocation and
redacted plan identity fields needed by recorded targeting, list and status.
Matching client validators and CLI renderers must accept this destroyed-only
exception without weakening validation for other lifecycle states or changing
unrequested ordinary inspect shapes. Already-shipped clients reject this tombstone
on inspect; they must upgrade to read it. No read-only request may trigger erasure.

**Approved destroy compatibility (ADR99-002):** reject older destroy callers
before effects. Require the narrow `DestroyRequest` field
`contract: "erase-attempt-history/v1"` on `/v1/destroy`, including force and
idempotent retries. Missing or unsupported values return the existing structured
`api_version` rejection with matching-client guidance before lifecycle admission,
provider/runtime/data effects, evidence deletion or durable mutation. Matching client destroy adapters supply the
marker; it acknowledges this fixed destroy contract, not optional erasure or new
cleanup authority. Preserve `force` semantics. Old clients omit it and are rejected;
old daemons reject the unknown field through existing strict decoding. Never retry
without the marker or fall back to legacy destruction. Keep the `/v1` namespace
and existing error envelope; neither a global API bump nor capability negotiation
is necessary. The state-version configuration preflight does not protect old
`/v1/destroy` calls. The owner approved this pre-effect rejection; matching clients must upgrade.

Implementation evidence must cover both encode/decode directions, absent/unknown
marker rejection with unchanged state/runtime/evidence, marked successful destroy
and retry returning the execution-free tombstone, failed/uncertain cleanup retaining
diagnosis, updated inspect/status/list rendering and rejection by an old daemon.
A lost mutation reply remains unknown acceptance/completion, never permission to
retry a different contract.

Before any state-format edit, capture a nonempty closed v11 database with the
unmodified pinned producer, fixture-only inputs, provenance and hashes. Add one
real cumulative migration after v11, retaining v9/v10 steps and fixtures. Seed only
the current known attempt and matching known provenance. Already-overwritten
results cannot be reconstructed from logs or timestamps. Missing old facts stay
unknown; legacy evidence observation time is not fabricated completion time.
Migration performs only transactional database work; evidence adaptation occurs
after open under existing ownership. Test oldest-supported migration through the
complete chain, rollback/reopen, and predecessor-current-result preservation.
Historical migration steps validate their source bucket layout, not the expanded
current layout; adding history must not make the retained v10-to-v11 step demand
a future bucket.

## Consequences

Maintained scope spans storage/migration, lifecycle writers, bounded read contracts,
log/evidence paths, export, destroy and daemon retention policy. History remains
active for all accepted executions even if nobody queries it. Each progress write
has an additional same-transaction history update; existing periodic retention
scans now visit bounded retained attempts and retry interrupted erasure. There is
no new dependency, goroutine owner, timer, monitoring service or provider authority.
Protected obligations remain potentially unbounded and require operator cleanup.

New query/projection validation, genuine predecessor fixtures and owned-runtime
success/failure evidence are necessary. Erasure is irreversible; guides/help must
change with implementation, and users must export before successful destroy.
Native cleanup confidence and unseen-descendant limitations remain unchanged.

Pre-history mixed logs/artifacts can contain overwritten attempts with no retained
result. Automatic age/byte expiry still covers those unrepresented bytes using the
instance's persisted completion/legacy observation clock, never file mtime or a
restart-renewed timestamp. Preserve represented protected/unexpired evidence and
all unknown bytes while any retained cleanup obligation makes the instance unsafe.
Existing deletion intents and legacy rewrite fences apply; unknown results remain
unavailable and no history/completeness ledger is fabricated. Manual/count pruning
continues to select retained history only.

## Alternatives considered

- Preserve only the last result: substantially cheaper but loses repeated failures
  and cannot satisfy approved paginated/exact history or original evidence export.
- Keep caller exports only: existing safe mechanism, but requires callers to race
  replacement and duplicates retention/selection outside daemon ownership.
- Copy whole instance/runtime journals: fewer projection decisions, but exposes
  secrets and capabilities and confuses diagnostic history with cleanup authority.
- Add SQL/event sourcing or another retention worker: unnecessary when existing
  bbolt transactions, lifecycle locks and expiry owner already provide boundaries.
- Rely on the state-version preflight for destroy compatibility: old destroy
  callers do not run it. A required request marker is narrower than global version
  negotiation and permits pre-effect rejection; permitting old callers to erase
  history and then fail response validation was rejected by the owner in favor of
  pre-effect rejection.
- Rewrite aggregate logs on every replacement: avoids a new path convention but
  adds repeated copying and cursor churn; attempt-scoped new files limit this to
  legacy compatibility and require no external-file effects during migration.

## Revisit when

Protected cleanup obligations cause material storage growth; query/write costs
measurably harm ordinary lifecycle latency; or consumers demonstrate a need for
transition replay rather than bounded attempt results. Change guarantees explicitly
before adding a journal, background service or historical secret access.

## References

- [Issue 99](https://github.com/octacian/backlot/issues/99)
- [Portable reports](20261018T233600000Z-publish-daemon-owned-portable-run-reports.md)
- [Recorded discovery](20261027T215913789Z-separate-declaration-discovery-from-bounded-recorded-inspection.md)
- [Image-pin retention](20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md)
- [Destroy request](../../api/v1/daemon.go), [strict handler](../../internal/daemon/server.go), [client validation](../../internal/client/validate.go)
- [Inherited waiver history](20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
- [Cumulative migrations](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Runtime writers](../../internal/daemon/runtime_state.go), [coordinator](../../internal/daemon/runtime.go), [cleanup](../../internal/daemon/resources.go)
- [Retention](../../internal/daemon/evidence.go), [log pages](../../internal/daemon/log_page.go), [export](../../internal/daemon/export.go)
