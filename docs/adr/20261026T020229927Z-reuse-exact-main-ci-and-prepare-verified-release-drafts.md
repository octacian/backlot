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

Reuse trusted exact-commit `main` CI and prepare a verified GitHub draft with
generated notes for human curation and publication. Incorporate the
[previous release decision](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md) in full,
except the repository-check and notes/publication changes below.

## Context

[Issue #85](https://github.com/octacian/backlot/issues/85) identifies duplicate
repository checks on tags whose exact source already passed main CI.
[Issue #86](https://github.com/octacian/backlot/issues/86) records skipped archive
smokes and publication on `v0.1.0` after an intentionally skipped `changes` ancestor.

## Decision

- Require trusted `main` push runs for the exact full tagged SHA, all completed
  with conclusion `success`, from the five workflow paths in the [release procedure](../preparing-a-release.md).
  Match workflow identity and repository/head-repository ownership. Each workflow's
  newest relevant run and latest attempt are authoritative, never an older success.
  Discovery must be complete and fail closed on missing, inaccessible, malformed,
  unknown, pending or adverse evidence, including skipped workflows. Successful
  workflows retain their existing legitimate job-level path skips.
- Require successful main CI preflight before packaging and recheck immediately
  before draft creation. These are bounded checks, not continuous validation of
  later reruns or edits. Retain the fresh final-archive packaging, native-smoke and
  exact uploaded-asset verification gates in the [workflow](../../.github/workflows/release.yml)
  and [script](../../.github/scripts/release.sh), uploading the smoked bytes without rebuilding.
- Make eligibility explicit across the skipped tag `changes` ancestor: smokes
  require successful packaging and no cancellation; drafting requires successful
  preflight, packaging and both native smokes and no cancellation. Failed,
  cancelled or unexpectedly skipped required gates block drafting. Preserve
  PR/main path selection and tag-only drafting; preflight receives Actions read access.
- Use GitHub-generated notes as the canonical per-release record in an unpublished
  verified draft. Keep durable compatibility/migration guidance in the repository;
  link tagged installation/upgrade, migration docs and schemas from the notes.
  Retain historical notes. A maintainer curates changes, targets, compatibility,
  known issues and evidence limits, confirms the mutable draft still has its
  verified assets, then manually publishes. Follow the [procedure](../preparing-a-release.md)
  for the ordinary notes baseline and failed-draft recovery.

## Consequences

Maintainers own the workflow list and must complete/rerun exact-SHA main CI when its
evidence is unavailable or unsuccessful. GitHub API access is required at both
gates; generated prose and subsequent draft edits remain a human review duty.

## Alternatives considered

- **Repeat repository checks on tags:** safe, but duplicates exact-source main CI.
- **Accept PR checks, an older success or only Check:** misses merged-source
  coverage or newer adverse evidence. Requiring every job would undo path selection.
- **Auto-publish or require checked-in version notes:** removes human curation or
  duplicates GitHub's record.

## Revisit when

Automatic workflows, their job-selection trust model or GitHub run/attempt semantics
change, or maintainers need automatic publication or continuous draft validation.

## References

- [Packaging checks and limits](../testing.md#release-packaging-and-publication), [release procedure and recovery](../preparing-a-release.md)
- [Guard tests](../../cmd/backlot/release_test.go), [original tag run](https://github.com/octacian/backlot/actions/runs/40254695599)
