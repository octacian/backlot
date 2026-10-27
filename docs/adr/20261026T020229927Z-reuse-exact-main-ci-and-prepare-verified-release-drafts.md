---
title: 'Reuse exact main CI and prepare verified release drafts'
date: '2026-10-26'
status: accepted
summary: 'Reuse trusted exact-commit main CI while freshly verifying release archives and leaving generated GitHub notes in an unpublished draft for human review.'
tags: [release, compatibility, tooling, ci]
supersedes: [20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md]
superseded_by: []
---

# Reuse exact main CI and prepare verified release drafts

Reuse trusted exact-commit `main` CI, freshly verify both release archives, and
prepare a GitHub draft with generated notes for human curation and publication.
This supersedes the [previous release decision](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md),
incorporating all its decisions except the repository-check and notes/publication
changes below. Its packaging, identity, targets, compatibility, state, upgrade,
tag restrictions, immutability and manual failed-draft recovery rules still apply.
Require clean exact-event source and matching local/remote tag resolution.

## Context

[Issue #85](https://github.com/octacian/backlot/issues/85) identifies duplicate
repository checks on tags whose exact source already passed merged-source CI.
[Issue #86](https://github.com/octacian/backlot/issues/86) records skipped archive
smokes and publication on `v0.1.0`: downstream implicit success conditions
propagated an intentionally skipped `changes` ancestor. Separately, the owner
chooses curated GitHub notes over mandatory checked-in per-version notes.

## Decision

- Require trusted `main` push runs for the exact full tagged SHA, all completed
  with conclusion `success`, from the five workflow paths in the [release procedure](../preparing-a-release.md).
  Match workflow identity and both repository and head-repository ownership;
  PR, tag, manual, fork and other-SHA evidence cannot qualify. Each workflow's
  newest relevant run and latest attempt are authoritative, never an older
  success. Discovery must be complete and fail closed on missing, inaccessible,
  malformed, unknown, pending or adverse evidence, including skipped workflows.
  Successful workflows may retain their existing legitimate job-level path skips.
- Require successful main CI preflight before packaging; recheck immediately before
  draft creation. These are bounded checks, not continuous validation of later
  reruns or edits.
  Freshly package and smoke both exact final archives on their native targets,
  retaining checksum, contents, identity, embedded-attribution and owned daemon
  lifecycle checks. Upload those same bytes without rebuilding; verify the exact
  uploaded asset set and downloaded bytes, draft status, tag and source identity.
- Make downstream eligibility explicit across the skipped tag `changes` ancestor:
  smokes require successful packaging and no cancellation; drafting requires
  successful preflight, packaging and both native smokes and no cancellation.
  A failed, cancelled or unexpectedly skipped required gate blocks drafting.
  Preserve PR/main path selection and tag-only drafting, per-tag serialization
  and final-job-only write permission; preflight receives Actions read access.
- Create an unpublished draft with GitHub-generated notes for the existing verified
  tag. Use GitHub's ordinary previous-release baseline and retain historical notes
  and the existing `v0.1.0` tag. Keep durable compatibility/migration guidance in
  the repository and link tagged installation/upgrade, migration docs and schemas
  from the notes. A maintainer curates changes, targets, compatibility, known issues
  and evidence limits, confirms the mutable draft still has its verified assets,
  then manually publishes. Existing-release refusal (including drafts), fail-closed
  lookup/upload verification and manual failed-draft recovery remain inherited.

## Consequences

Repository checks are reused while artifact verification stays fresh. Maintainers
own the explicit workflow list and must complete/rerun exact-SHA main CI when its
evidence is unavailable or unsuccessful. The draft hands off verified assets;
generated prose still needs review, and later edits/reruns are outside the checks.
The next owner-created tag must establish the live tag-to-draft path; local fixtures
and ordinary CI cannot prove it. Inherited unsigned-artifact and recovery limits remain.

## Alternatives considered

- **Repeat repository checks on tags:** safe, but duplicates exact-source main CI.
- **Accept PR checks, an older success or only Check:** misses merged-source
  coverage or newer adverse evidence. Requiring every job would undo path selection.
- **Auto-publish or require checked-in version notes:** removes human curation or
  duplicates GitHub's record. Keep durable migration guidance in source instead.
- **Add a release service or automatic recovery:** unnecessary state and write
  authority for a bounded operator action handled by existing tooling.

## Revisit when

Automatic workflows, their job-selection trust model or GitHub run/attempt semantics
change, or publication, draft invalidation, targets, signing or recovery needs change.

## References

- [Release workflow](../../.github/workflows/release.yml), [release script](../../.github/scripts/release.sh), [guard tests](../../cmd/backlot/release_test.go)
- [Packaging checks and limits](../testing.md#release-packaging-and-publication), [release procedure and recovery](../preparing-a-release.md)
- [Original tag run](https://github.com/octacian/backlot/actions/runs/40254695599)
