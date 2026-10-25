---
title: "Publish immutable versioned releases with GoReleaser"
date: 2026-10-23
status: accepted
summary: 'Use pinned GoReleaser OSS archives and a gated GitHub draft publication to distribute identifiable releases without a packaging framework.'
tags: [release, compatibility, tooling, ci]
supersedes: []
superseded_by: []
---

# Publish immutable versioned releases with GoReleaser

Use pinned GoReleaser OSS for reproducible macOS ARM64 and Linux amd64 archives,
with existing dependency notices and version discovery. Publish stable `v0.x.y`
tags through fresh checks and native archive smoke tests, then expose a complete
GitHub Release. Published tags and assets never change. Preparing this automation
does not authorize creating a tag or publishing a release.

## Context

[Issue #25](https://github.com/octacian/backlot/issues/25) needs a small distribution
path for early adopters. The Makefile already builds reproducible binaries and
verifies notices; the CLI has a typed version response but defaults to `dev`.
There was no archive/publication pipeline. The initial V1 plan also imposed a
manual MVP adoption cycle beyond CI. The owner retires that blanket requirement
for releases and development/PR acceptance; V1 described the initial product
scope, not a separate qualification or a `1.0.0` stability promise. Go-equipped
users can already install the command. Preserve that path and existing runtime/state compatibility rather
than adding an installer, updater or release service.

## Decision

- Use one pinned GoReleaser OSS configuration for exactly `darwin/arm64` and
  `linux/amd64` (baseline amd64), producing
  `backlot_v0.x.y_GOOS_GOARCH.tar.gz` and a versioned SHA-256 checksum file.
  Each archive contains executable `backlot`, `LICENSE` and the verified
  `THIRD_PARTY_NOTICES.md`. Every executable also embeds those canonical files,
  including ordinary builds and Go installations; `backlot licenses` prints their
  full text without adjacent files. Retain `make release-build` as the plain
  binary path with equivalent deterministic build settings. Do not introduce a packaging engine.
- Release identity is the exact tag, full commit and commit UTC time, supplied
  through the existing linker variables. Fix the Go toolchain, module inputs,
  build flags and archive metadata; disable cgo, local paths, automatic VCS
  stamping, build IDs and wall-clock inputs. Equal source, identity and tooling
  must produce equal binaries and archives, including from independent trees.
- Fall back to Go's embedded build information for `go install`: expose the
  resolved module version, including a pseudo-version for an untagged revision,
  and revision when available. A tagged module version identifies tagged source
  even if Go supplies no full revision. Do not invent a hash or build timestamp;
  local development and dirty builds must not claim a clean official release.
  Preserve the existing version JSON fields and API/state version meanings.
- Trigger publication only on a pushed canonical stable `v0.x.y` tag (numeric
  components without leading zeros); reject prereleases and build suffixes.
  Resolve and check out the event's exact tagged commit with full tag history,
  verify the remote tag still resolves to it, and run fresh required repository
  checks on both native targets without changed-path skips. Build both archives
  with GoReleaser's publish step disabled. Run checksum, extraction, identity and
  private owned daemon lifecycle smoke checks on each target using those exact
  archives. Both targets must pass before publication; do not rebuild afterward.
- Keep publication in one serialized job per tag, with write permission confined
  to that job. Use GitHub CLI to refuse an existing release (including a draft),
  create a draft for the already-existing verified tag, upload only the complete
  checked asset set and reviewed release notes, and publish only after confirming
  the uploaded set. Fail closed on lookup/verification/upload errors. Never
  clobber assets, move tags or automatically repair partial releases. An operator
  may inspect and explicitly discard an unpublished failed draft before retrying
  the unchanged tag; published corrections require a new patch version. Repository
  immutable-release/tag protections are additional operator safeguards, not an
  assumed precondition or permission to change repository settings here.
- Release readiness requires reviewed notes and applicable required CI passing
  at the exact reviewed revision to be published. Tag automation repeats fresh
  repository/build/native archive and embedded-attribution checks. Development,
  PR acceptance and routine releases do not require a duplicate local full
  provider, SSH, system-trust or four-workflow adoption cycle, nor a separate V1
  qualification. Select manual checks only where a change introduces risk to a
  guarantee without CI coverage; the [diagnostic fixtures](../release-acceptance/README.md)
  remain available for that purpose. This retires the obsolete manual MVP gate;
  it does not claim that unexecuted checks passed or expand platform evidence.
  This policy governs release/PR readiness where older records prescribe blanket
  validation cycles; their runtime/platform guarantees and historical evidence
  remain unchanged.
  Notes identify changes, supported targets, known issues, evidence limits and
  upgrade/compatibility effects. Ship tagged docs and schemas with the source;
  link their exact tag from release instructions.
- `v0.1.x` patches preserve documented CLI invocation/exit behavior, JSON/local
  API contracts, manifest/machine configuration and retained-state upgrade
  handling; breaking changes require a new `0.x` minor with migration notes.
  Release, wire/schema and persisted-state versions remain independent. Reserve
  `1.0.0` for an intentional stable public API commitment.
- Recommend a CLI and running daemon from the same release; existing API/state
  compatibility checks remain authoritative, without promising arbitrary mixed
  release pairings. Stop the old daemon with its matching binary before replacing
  it, wait for exit/lock release, then explicitly start the new binary. Preserve
  state and CA identity. Retain the accepted forward migration policy from v9;
  downgrades/reversible migrations are not promised and unsupported state stays
  unchanged for matching-binary recovery.
- Document `go install ...@latest` as convenient unpinned installation, exact
  `@v0.1.0` after publication, PATH, checksum/download/extract/user installation,
  upgrade and binary removal without implicit state/CA deletion. `main` and
  `@latest` are moving selections. Caddy, dependencies and trust setup stay explicit.
  Exclude Windows artifacts, prereleases, package-manager taps, installers,
  signing/notarization, self-updates and runtime/data-format changes from this slice.

## Consequences

This adds a build-time tool pin, declarative archive configuration and a small
workflow boundary around existing checks and GitHub CLI. It adds no runtime
dependency, persistence, API endpoint or background activity. Maintainers own
tool updates, release notes, release timing, targeted manual checks for uncovered
change risks, and failed-draft decisions. CI-led readiness avoids repeating a
manual MVP ceremony, but CI establishes only the guarantees it exercises; report
uncovered risks and historical failures honestly.
GitHub publication is not a transaction: a failed upload can leave a private
draft requiring inspection. Checksums detect corruption, not publisher identity;
these archives are unsigned and unnotarized. Public tagged Go installation can
only be verified once an actual tag exists; preparation must say so explicitly.

## Alternatives considered

- **Repeat the initial manual MVP cycle for every release or V1 declaration.**
  Adds operator/provider/trust setup and duplicate full-suite runs regardless of
  the changed behavior. The owner instead chooses required CI with targeted
  manual checks for uncovered change risks; product guarantees remain intact.

- **Only Go installation.** Already sufficient for developer adoption but does
  not deliver the requested ordinary downloadable binaries/checksums.
- **Extend Make with custom tar/checksum/publication code.** Avoids a tool pin,
  but makes us maintain cross-platform archive normalization and packaging rules
  already supplied by GoReleaser OSS. Keep Make's existing binary/notices paths.
- **Let GoReleaser publish directly.** Less workflow glue, but its publication
  lifecycle does not itself establish native smoke of both final archives before
  exposure. Separate nonpublishing packaging and GitHub draft publication keeps
  that gate explicit without Pro split/merge, a custom framework or a new service.

## Revisit when

Revisit when actual adoption requires another verified target, signing,
installation automation or prereleases; when GoReleaser stops reducing maintained
code; or when publication/recovery needs exceed one bounded draft attempt.

## References

- [Issue #25](https://github.com/octacian/backlot/issues/25)
- [Build and notices](../../Makefile), [CLI metadata](../../cmd/backlot/main.go), [version response](../../api/v1/version.go)
- [Installation](../operations.md), [platforms](../platforms.md), [testing](../testing.md)
- [Retained state decision](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [GoReleaser Go builds and reproducibility](https://goreleaser.com/customization/builds/builders/go/), [archives](https://goreleaser.com/customization/package/archives/), [checksums](https://goreleaser.com/customization/package/checksum/)
- [GoReleaser nonpublishing release](https://goreleaser.com/getting-started/quick-start/#release-flags), [GitHub release creation](https://cli.github.com/manual/gh_release_create)
- [Go embedded build information](https://pkg.go.dev/runtime/debug#BuildInfo), [SemVer](https://semver.org/)
