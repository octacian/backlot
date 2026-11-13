---
title: "Confirm recorded checkout cleanup before streaming destruction"
date: 2026-11-13
status: accepted
implementation: in-progress
summary: "Authorize a reviewed recorded target set, fence its checkout admission, and stream cancellable destruction through existing ownership journals."
tags: [cleanup, lifecycle, ownership, cli, api]
supersedes: []
superseded_by: []
related: [/docs/adr/20261110T181436860Z-organize-management-by-daemon-checkout-and-persistent-scene.md, /docs/adr/20261111T214921188Z-stream-workload-wide-stop-outcomes-without-a-fixed-scope-ceiling.md, /docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261106T230242771Z-recover-device-only-bindings-after-verified-consumer-settlement.md]
tracking: [https://github.com/octacian/backlot/issues/132]
implemented_by: []
---

# Confirm recorded checkout cleanup before streaming destruction

## Context and Problem Statement

Removing a checkout leaves recorded instances, owned data, shared allocations and
evidence behind. Discovery pages and shell loops cannot freeze a reviewed cleanup
scope or explain partial completion. Operators need explicit consent, live progress
and cancellation while preserving authenticated destruction and crash recovery.

## Considered Options

- Continue exact-ID destruction: existing authority and recovery, but manual scope
  selection and no coherent batch consent.
- Apply a query after generic confirmation: simple, but can include newly admitted
  work that the operator did not review.
- Bind confirmation to a revalidated preview: usable human confirmation and exact
  machine consent, reusing per-instance journals; chosen.
- Require Git-removal proof or a missing-path age threshold: excludes legitimate
  deleted checkouts without establishing resource ownership.
- Persist a resumable batch journal: adds migration, replay and retention duties
  beyond explicit inspection and retry.

## Decision Outcome

Bind batch consent to a revalidated preview and coordinate destruction through
scoped admission fences and existing per-instance ownership journals.

### Selection and consent

Add `checkout destroy` and `orphan cleanup` within one selected daemon/state root.
Checkout destruction selects recorded instances under the immutable checkout
parent, including running, stopped, interrupted, kept and settled instances,
dependent task evidence and destroyed identities with pending authorized evidence
erasure. Existing paths use current checkout-selection rules; recorded checkout IDs
also work after directory removal. Selection uses recorded ownership, not host-wide
process or Docker discovery.

An orphan candidate has a valid recorded checkout association, an absent current
bound path, no live execution/preparation/expiry owner and proven settled consumers.
Permission/inspection errors, identity conflicts, staged reassociation and uncertain
runtime/data cleanup are blockers. Show eligible candidates and blocked records
separately. Authorized evidence-only erasure remains retryable. Preview and apply
provide sufficient consent for missing paths, including unplugged volumes; stronger
volume detection is deferred unless existing reliable evidence suffices.

Preview is read-only. Show runtime effects and permanent loss of owned data,
allocations and results/logs/artifacts, preserved shared servers, caller exports and
unowned data, and export-first guidance with partial-export requirements/blockers.
Keep capabilities and private configuration out of public results.

Bind the daemon-produced digest to daemon generation, normalized scope, checkout
binding revisions, exact targets, attempts/resource generations and relevant
loss/eligibility facts. It establishes consent and freshness, not cleanup authority;
freshness depends on state rather than clock expiry. Human terminal mode previews
then asks `Apply this cleanup? [y/N]`, sending the digest on acceptance. JSON and
noninteractive human mode only preview. `--apply-preview DIGEST` explicitly applies
in either mode. Decline, EOF or interruption before acceptance leaves state intact.

### Coordination and outcomes

Before effects, fence the selected checkout parents and recompute the preview under
existing admission/binding serialization. Changed scope, bindings, generations or
blockers refuse application. Preparation, execution and task acceptance check the
fence at final publication; invalidated checkout-scoped tickets stay invalid after
reopening. Unrelated checkout tickets remain usable. Reassociation and registration
rotation check affected fences at publication, holding binding serialization only
for publication. Stop/join selected owners through existing lifecycle paths.

Use evidence-before-instance lock ordering. After acquiring target locks, reobserve
identity, binding, generation and relevant eligibility/loss facts before destructive
effects. In particular, recheck orphan path absence and active-consumer exclusion
when each target is reached; changed prerequisites leave it unresolved. Join
dependents before parent data release. Reuse existing ownership proofs, allocation
release and evidence-erasure journals without an aggregate force override. Continue
independent reviewed targets after failure. Shared servers retain their explicit
`shared destroy` boundary; exports and unrelated owners remain protected.

Stream typed preview/progress, acceptance, per-target/resource observations and a
final summary; human output renders the same facts. Distinguish done, failed,
unresolved and cancellation-unattempted work. There is no total deadline or target
ceiling; compact state/output may grow with scope. Trust-boundary validation and
individual provider, stop-grace and owner-join limits remain.

Accepted cleanup has a registered daemon owner independent of output delivery.
Explicit operation-scoped cancellation stops scheduling targets and is honored
before the current target's next destructive step. An indivisible started effect
may finish while retaining ownership through completion or durable retry state.
CLI interruption requests cancellation and distinguishes acknowledgement from
unknown delivery. Slow output cannot block cleanup/cancellation; lost output proves
neither cancellation nor completion. Shutdown cancels/joins registered owners;
release the scoped admission fence when coordination safely settles.

### Interrupted directory removal

Reuse resource/evidence journals, recording intent before effects and completion
afterward. Shared directory destruction, including exact-ID cleanup, must preserve
verifiable authority through partial removal. Durably preserve the verified original
ownership authority before deleting contents. Retain the in-root marker until a
durable marker-only root exists. Preserve that original marker as an exclusively
published, durable private sibling hard-link witness before unlinking it. Validate
the token, private directory, journal and expected resource path.

Without the in-root marker, the witness authorizes only nonrecursive removal of an
empty, non-symlink root at that journal's private location. Nonempty or ambiguous
replacements remain unresolved. Never reconstruct a missing marker or recursively
delete using witness-only authority. Establish durable root absence before witness
removal and durable witness removal before journal completion. Failed durability
retains retry authority or is reported unresolved. Witnesses remain private.

After cancellation or daemon/power loss, inspect residual state and explicitly
preview/retry remaining cleanup; completed deletions are irreversible. Existing
journals support recovery rather than a durable batch journal or stream replay.
Require a supported wire contract before effects; incompatible peers fail without
fallback or automatic mutation retry.

### Consequences

The feature adds typed API/client contracts, a transient coordinator, scoped checks
at existing writers and one temporary durable witness per directory reaching
terminal removal. It adds no dependency, database migration, sweeper or work when
unused. Unsupported hard-link or sync operations leave cleanup unresolved.

Explicit orphan application can delete retained Backlot data for an unplugged
volume; preview exposes the missing-path evidence. The new consent flow applies to
these batch commands; existing exact-ID and scene interfaces remain. Revising
`stop --all` interruption is separately tracked by issue #145.
