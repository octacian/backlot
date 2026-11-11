---
title: 'Reuse exact main CI and prepare verified release drafts'
date: '2026-10-26'
status: accepted
implementation: implemented
summary: 'Reuse trusted exact-commit main CI while freshly verifying release archives and leaving generated GitHub notes in an unpublished draft for human review.'
tags: [release, compatibility, tooling, ci]
supersedes: [/docs/adr/20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md]
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/85", "https://github.com/octacian/backlot/issues/86"]
implemented_by: ["https://github.com/octacian/backlot/commit/21d16765c6c6f01e06518fcbbd2db3f4e312a044"]
---

# Reuse exact main CI and prepare verified release drafts

## Context and Problem Statement

Tag builds repeat repository checks already passed by the exact main source.
An earlier release also skipped final archive smoke gates after an intentionally
skipped changes ancestor. Publication must verify final assets without unnecessary
full-suite repetition or automatic release exposure.

## Considered Options

- Reuse trusted exact-main CI and retain fresh archive/asset verification.
- Repeat repository checks on tags: safe but duplicates exact-source coverage.
- Accept PR/older/partial successes: misses merged coverage or newer adverse evidence.
- Auto-publish or checked-in version notes: removes human curation or duplicates
  the canonical GitHub release record.

## Decision Outcome

Require complete trusted main-push CI for the exact tagged SHA from the required
workflow identities. Repository ownership, newest relevant runs and latest attempts
matter; missing, inaccessible, malformed, pending or adverse evidence fails closed.
Legitimate successful workflow-level outcomes may include their normal job skips;
a skipped required workflow is not success.

Check eligibility before packaging and again immediately before draft creation.
Keep fresh native smoke of both exact final archives and verification of the uploaded
asset set/bytes, without rebuilding. Required packaging/smokes/preflight must succeed
across intentionally skipped ancestors; failure/cancellation/unexpected skip blocks
drafting. The [release procedure](/docs/preparing-a-release.md) owns the workflow list.

Create a verified unpublished draft with generated notes and tagged compatibility/
installation/schema links. A maintainer curates notes, reconfirms the mutable draft
assets and publishes manually. Keep the prior reproducibility, immutability and
failed-draft safeguards; this replaces only duplicate repository checks and the
notes/publication steps.

### Consequences

Maintainers own workflow-list changes, exact-SHA reruns when evidence is unavailable,
GitHub API availability and final publication review. Bounded preflight does not
continuously validate later reruns or draft edits. No release service or new runtime
boundary is introduced.

## More Information

[Release guard](/.github/scripts/release.sh), [guard tests](/cmd/backlot/release_test.go).
