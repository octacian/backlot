---
title: "Publish immutable versioned releases with GoReleaser"
date: 2026-10-23
status: superseded
implementation: implemented
summary: 'Use pinned GoReleaser OSS archives and a gated GitHub draft publication to distribute identifiable releases without a packaging framework.'
tags: [release, compatibility, tooling, ci]
supersedes: []
superseded_by: [/docs/adr/20261026T020229927Z-reuse-exact-main-ci-and-prepare-verified-release-drafts.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/25"]
implemented_by: ["https://github.com/octacian/backlot/commit/92703b5cf52c51c7a539926a840a499c3ba8a0e7"]
---

# Publish immutable versioned releases with GoReleaser

## Context and Problem Statement

Early adopters need identifiable downloads as well as Go installation. Existing
build/notices paths should remain useful without a custom packaging engine,
installer, updater or release service.

## Considered Options

- Pinned GoReleaser OSS packaging plus separately gated GitHub publication.
- Go installation only: does not provide ordinary archives/checksums.
- Handwritten archive/checksum logic: duplicates cross-platform normalization.
- Direct tool publication: does not establish native smoke of both final archives
  before exposure.

## Decision Outcome

Produce reproducible macOS ARM64 and Linux amd64 archives with executable, license,
notices and SHA-256 checksums. Embed attribution in every executable. Fixed source,
release identity and tooling must produce equal bytes; never invent revisions or
build times, and never represent dirty development builds as clean official releases.
Go installation reports available embedded module/build identity.

Publish only canonical stable tags at their verified exact source. Package without
publication, smoke the exact final archives on both native targets, then upload
those bytes without rebuilding. Confine write authority to serialized publication;
refuse existing releases/drafts and fail closed on incomplete lookup/upload checks.
Published tags/assets never change; corrections require a new version. Failed
unpublished drafts require explicit operator inspection before retry.

Keep release, API/schema and state versions independent. Patch releases preserve
documented contracts; breaking changes require a new minor and migration guidance.
Retain cumulative state upgrades, matching-daemon lifecycle and CA/data preservation.
No arbitrary mixed-release pairing or downgrade guarantee follows from distribution.

The successor replaces duplicated tag repository checks with trusted exact-main CI
and leaves verified drafts unpublished for human curation. Other packaging and
immutability guarantees remain.

### Consequences

Maintainers own tool pins, notes, targeted uncovered-risk checks and failed drafts;
there is no runtime dependency or background updater. Publication is not transactional
and checksums establish integrity rather than publisher identity. Signing,
notarization, more targets or installers require demonstrated demand.

## More Information

[Release procedure](/docs/preparing-a-release.md), [packaging](/.goreleaser.yaml),
[GoReleaser reproducibility](https://goreleaser.com/customization/builds/builders/go/).
