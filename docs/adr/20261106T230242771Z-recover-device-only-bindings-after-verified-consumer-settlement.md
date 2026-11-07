---
title: "Recognize local projects through durable registration markers"
date: 2026-11-06
status: accepted
implementation: not-started
summary: "Separate logical checkout and common-anchor registration from physical source checks, preserving immutable owners and explicit repair."
tags: [identity, recovery, ownership, state, configuration]
supersedes: [/docs/adr/20261103T174043965Z-reassociate-retained-projects-through-explicit-current-bindings.md]
superseded_by: []
related: [/docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md, /docs/adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
tracking: [https://github.com/octacian/backlot/issues/125, https://github.com/octacian/backlot/issues/128]
implemented_by: []
---

# Recognize local projects through durable registration markers

## Context and Problem Statement

One reported reboot changed device numbers, requiring separate repairs for a main
checkout and linked worktrees. Physical filesystem observations currently determine
long-term association as well as guarding individual source reads. Developers need
stable local association without coordinating every peer scene after a reboot.
The incident has not been independently reproduced.

The owner accepted logical markers under the local development trust model:
copied administrative metadata at the same registered canonical path means
continuity, including existing private inputs. Normal clones start fresh. Detecting
hostile same-user replacement or exact same-path metadata restoration is outside
this guarantee.

## Considered Options

- Retain explicit repair for every filesystem identity change: least new code,
  but repeats manual work and does not meet the requested continuity.
- Persist filesystem UUID/inode/birth observations: stronger replacement detection,
  but adds platform collectors, filesystem limits and enrollment obligations that
  the accepted local trust policy does not require.
- Use canonical paths alone: simpler, but silently associates fresh clones at old
  locations without the administrative continuity witness.
- Local registration markers plus stored path claims: portable and independent of
  device numbering; chosen with explicit duplicate and legacy handling.

## Decision Outcome

Replace long-term physical matching with logical registration, retaining
operation-local physical validation and immutable resource ownership. Supersede the
current-binding decision's automatic-recognition prohibition and shared-owner-only
repair restriction; its other guarantees remain in force at actual reassociation.

### Registration and ownership

Use a random common-anchor ID in the resolved Git common directory and a separate
random checkout ID in each checkout's Git administrative directory. Main checkouts
need two distinct files even when those directories coincide. Linked worktrees
share the common ID and have different checkout IDs. Non-Git projects use two files
in local project metadata. Files are untracked local administrative state, never a
committed project token; no global Git configuration or ignore-file edit is needed.

IDs are neither secrets, capabilities nor daemon owner IDs. Each selected store
maps registration and canonical location to its existing immutable owner or private
association. Keep checkout, admin/common paths, Git links/classification, relative
project/manifest locations and declared project checks. Multiple projects remain
distinct. Preserve origins, scene/resource parents, allocations, credentials,
private generations, launch provenance and recorded cleanup authority; never rekey
old allocations or infer another store's authority from a marker.

The daemon remains the sole bbolt writer; explicit setup remains the private
registry writer. Both use one shared marker reader/publisher. Only existing mutating
setup/prepare paths enroll. Read-only plan/doctor/status/diagnose do not write;
source-only commands do not require enrollment. Already enrolled read-only sources
work. Missing, invalid or unwritable markers produce explicit enrollment/repair
results; exact legacy reads retain existing behavior without claiming continuity.
A missing marker at a retained path cannot silently enroll a replacement.

Publish each fresh marker as complete versioned bytes: create an exclusive sibling
temporary file, write, sync and close, hard-link to the final name without replacing
an existing file, sync the directory, then reread the winner. Concurrent setup and
prepare use that winner; reject malformed/nonregular markers. Unsupported link or
sync operations fail explicitly. Capture and revalidate paths, markers and source
before store acceptance under existing admission locks. There is no transaction
across marker files, registry and daemon database: interruption can leave unused
markers or one of the two roles published, granting no retained association.
Directory-sync uncertainty requires inspection, not a success claim or blind retry.

### Recognition and explicit resolution

An enrolled marker at its registered canonical location selects the same owner and
private association without changing binding revisions or recomposing/stopping
peers. An unavailable sibling does not prevent this recognition. Keep registry
selection/provenance, private-document validation and generation/digest freshness;
record current observations separately from historical provenance. Keep physical
source replacement checks during preparation, launch and tasks. Logical recognition
alone does not authorize launch, replacement, reassociation or destruction: existing
compatibility, admission, joins, capability checks and resource fences still apply.

Different-path duplicate IDs conflict against claims in the selected store only.
Offer an interactive choice of which exact checkout receives a new ID and an
explicit selected-path preview/apply equivalent for noninteractive use. Structured
conflicts identify roles, paths, retained owners, blockers and the next action.
No silent identity move, global duplicate scan or private-input import is allowed.
If a copied Git common directory also duplicates the common ID, fresh identity
separates that common association too; a genuine linked worktree keeps its shared
common association. The preview must expose this distinction and affected claims.

Either path may be selected. Its new marker never changes old allocation parents.
If selecting the original path changes its registration mapping, preserve its
retained owners there through explicit exact-owner repair; the copy retaining the
old marker must receive fresh owners and explicit private setup, never the old
path's secrets. Stop/join and fence consumers affected by a real reassignment;
normal recognition does not stop peers. Missing or ambiguous originals require
explicit owner resolution, never deletion or adoption. Revalidate the reviewed
claims under existing locks; marker replacement is complete and serialized, with
store publication staged separately. Cooperating writers serialize rotation of
the same marker and recheck its reviewed expected-old ID immediately before
replacement, in a consistent order with store locks and outside bbolt transactions.
Interrupted staging leaves an actionable
mismatch requiring inspection/exact-owner repair, not automatic remapping.

Exact validated legacy mutating use may enroll an existing binding. Private legacy
enrollment requires explicit setup. Mismatched legacy records require acknowledged
exact-checkout repair and explicit private setup as applicable. Extend repair to
checkout-only records without fabricating a shared owner; include real retained
shared obligations when present. Retain origin reservations and non-destructive
fallbacks. Registry/state format upgrades must preserve legacy evidence as unknown
and reject unsupported newer writers.

### Interrupted work

Keep historical interrupted execution and collection gaps separate from current,
attempt-bound consumer settlement. Unknown or surviving effects retain fences;
independently verified release can settle work without rewriting its outcome.
Stop/status report settlement truthfully. Record causes with actual shared-service
status transitions, preserving existing retention/erasure rules. No device-repair
event history or new audit subsystem is needed for unchanged logical recognition.

### Consequences

Markers remove long-term filesystem collectors, mount parsing, observation enrollment
and automatic all-peer repair/recomposition. They add local file publication,
store mappings/versioning and explicit conflict/repair handling. Minimum typed
registration/enrollment/conflict/repair results must remain secret-safe. No new
dependency, background worker, timer, replay queue or operator pruning duty is
introduced. Settlement remains necessary at actual replacement/repair boundaries;
it is not a prerequisite for ordinary marker recognition. Infrastructure control
and bulk activity/stop interfaces remain outside this decision.
