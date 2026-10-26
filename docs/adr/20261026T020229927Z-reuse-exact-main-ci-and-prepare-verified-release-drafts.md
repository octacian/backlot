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

Use successful trusted `main` push CI for the exact tagged commit instead of
repeating repository checks on tags. Still build and smoke both final archives
fresh, then create and verify an unpublished GitHub draft with generated notes;
a maintainer curates the notes and explicitly publishes it.

This replaces the [previous release decision](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md).
The reproducibility, identity, target, compatibility, attribution and immutability
rules below retain its guarantees.

## Context

[Issue #85](https://github.com/octacian/backlot/issues/85) identifies repeated
macOS/Ubuntu repository checks on a tag whose exact source already passed main
CI. Main push CI independently covers repository checks, documentation, application
scenes, project inputs and archive validation. PR evidence cannot substitute for
that merged source. Reuse should reduce duplicate execution without accepting an
older success over newer adverse evidence.

[Issue #86](https://github.com/octacian/backlot/issues/86) records the `v0.1.0`
tag run whose packaging passed but archive smokes and publication were skipped:
`changes` intentionally skips tags, and downstream implicit success conditions
propagate that skipped ancestor. A workflow-level success alone did not establish
a complete tag release. Fixing eligibility must not let failed packaging or native
smokes reach draft creation.

The owner additionally chooses GitHub-generated notes as the canonical per-release
record, followed by human curation and manual publication. Requiring a checked-in
`docs/releases/<tag>.md` duplicates that record. Compatibility and migration
instructions must remain durable repository documentation, linked from the notes.
Historical notes and the existing `v0.1.0` tag remain untouched.

## Decision

- Keep pinned GoReleaser OSS, exactly `darwin/arm64` and `linux/amd64` archives,
  versioned SHA-256 files, canonical license/notices in archives and executables,
  existing Go installation metadata fallback, deterministic build settings and
  unchanged CLI/API/state version contracts. Identical source, identity and
  tooling must produce identical binaries and archives. Add no runtime behavior,
  service, dependency framework, installer, updater or new target.
- Accept only canonical stable `v0.x.y` tags with numeric components and no leading
  zeros. Check out the exact event commit, require clean source, and verify local
  and remote tag resolution against it. Never create or move tags in automation.
- Replace the tag-only repository-check matrix with a bounded read-only GitHub
  Actions preflight using existing `gh`, `jq` and Bash. Require these five workflow
  paths in this repository: `.github/workflows/check.yml` (Check), `docs.yml`
  (Docs), `scenes.yml` (Application scenes), `project-inputs.yml` (Current-head
  project inputs), and `release.yml` (Release archives), all under
  `.github/workflows/`. Match workflow identity/path, repository and head repository,
  event `push`, branch `main`, and the exact full tagged `head_sha`. PR, tag,
  manual, fork and other-SHA runs cannot satisfy the gate.
- For each workflow, select the newest relevant run by run number, independently
  of its conclusion, and use its latest attempt. Paginate run discovery rather
  than stopping at an arbitrary first page; refresh selected run metadata to
  observe its current attempt/status. Require every selected run to be completed
  with conclusion `success`. Missing, unknown, malformed, pending, failed,
  cancelled, skipped or inaccessible workflow evidence blocks drafting. Never
  search backward for a successful run or attempt. Report the workflow, run and
  attempt when available, and direct the maintainer to complete/rerun main CI
  before rerunning the tag workflow.
- Trust the existing reviewed main workflow definitions' job-level changed-path
  selection. A successful workflow may contain its legitimate path-based skips;
  do not require every job to run or reconstruct changed paths in the preflight.
  In particular main Release archives intentionally skips tag-only checks and
  drafting. A skipped workflow is not acceptable evidence. Retain the existing
  detection-failure behavior and filters; no new skip mode or override is added.
- Gate fresh tag packaging on successful preflight. Keep GoReleaser configuration
  and notice checks, both native final-archive smokes, checksums, exact file lists,
  executable identity/source time, standalone embedded attribution and the owned
  installed-binary daemon lifecycle harness. Reuse the same packaged bytes for
  upload; do not rebuild after smoke. Main Release archives success is evidence
  of main CI, not a substitute for fresh tag archives.
- Make downstream eligibility explicit across the intentionally skipped tag
  `changes` ancestor. Each smoke requires successful packaging and no cancellation;
  the tag-only draft job requires successful preflight, packaging and both native
  smokes and no cancellation. A status override only permits evaluating these
  prerequisites; failure, cancellation or unexpected skip in a required tag gate
  must block drafting. Preserve existing PR/main path selection and ensure these
  events never create a release.
- Retain serialization per tag and confine `contents: write` to the final draft
  job; grant only the Actions read access needed by preflight. Recheck authoritative
  main CI immediately before draft creation as well as before packaging, so a
  newer failed or pending attempt during packaging is not knowingly ignored.
  This is evidence at the gate, not a lock on future main CI reruns; no polling,
  retry service or continuous draft invalidation is introduced.
- Refuse every existing release, including drafts, and fail closed on lookup
  errors. Use GitHub-generated release notes for the already-existing verified
  tag (`gh release create --verify-tag --draft --generate-notes`), adding links
  to tagged installation/upgrade, compatibility/migration documentation and schemas.
  Let GitHub choose the ordinary previous-release baseline; do not special-case
  the existing unreleased `v0.1.0` tag or require a per-version repository file.
  Retain existing historical notes without rewriting them.
- Verify the exact uploaded asset names and downloaded bytes against the smoked
  artifacts, draft status, tag identity and source identity before reporting
  success. Leave the release as a draft: automation must never clear draft status.
  A maintainer reviews generated changes, targets, compatibility/migration links,
  known issues and evidence limits, then manually publishes. Human curation owns
  prose completeness; generated text does not establish compatibility or testing.
- Keep one bounded attempt and manual failed-draft recovery. Errors may leave an
  unpublished partial draft; inspect it and explicitly discard only that failed
  unpublished draft before retrying the unchanged tag. Never clobber assets or
  automatically repair, delete or publish a release. Published tags/assets remain
  immutable; corrections require a new patch version. Repository protections
  remain separate operator decisions.
- Retain `v0.1.x` patch compatibility for documented CLI/exit behavior, JSON/local
  API, manifests/machine configuration and retained-state upgrades. Breaking
  changes require a new `0.x` minor and durable migration guidance linked from
  its notes; `1.0.0` requires an intentional stable API decision. Recommend matching
  CLI/daemon releases; stop the old daemon with its matching binary, await exit
  and lock release, then replace and explicitly restart, retaining state and CA.
  Preserve forward migration from v9, unchanged unsupported-state recovery and
  no downgrade promise. Keep install/PATH/checksum/removal guidance in repository
  docs; binary removal must not implicitly delete state or CA identity.

## Consequences

Repository checks run once on the merged source while release-specific artifact
verification stays fresh. This adds a small read-only API policy boundary and
focused fixture coverage to the current script/workflow, not a new orchestrator.
Maintainers must update the explicit workflow list if automatic CI changes and
rerun main CI when evidence is missing or unsuccessful. Unavailable GitHub API
access blocks release preparation rather than weakening checks. There is no
background activity when a release is not requested.

The draft is a handoff, not a public release or proof of maintainer review. Partial
uploads still require manual inspection, and maintainers with write access can
modify a draft after automation verifies it; manual publication therefore owns
checking that the curated draft still has its verified assets. Checksums establish
byte integrity, not publisher authenticity; unsigned/unnotarized artifacts remain
an explicit limit. Public tagged Go installation remains verifiable only after
actual publication. Normal CI cannot prove the complete live tag-to-draft path;
the next owner-created tag supplies that operational evidence.

## Alternatives considered

- **Keep fresh full repository checks on every tag.** Remains safe but duplicates
  exact-source main CI and adds time and intermittent failure exposure. Fresh
  artifact-specific checks address the distinct tag risk more directly.
- **Accept PR checks, any prior success, or only Check.** Smaller discovery but
  cannot establish the owner's five-workflow merged-source policy or account for
  newer failures. Explicit workflow selection and latest evidence are justified.
- **Require every main job to succeed without skips.** Would force irrelevant
  suites and tag-only jobs on every main change, undoing established path selection.
  Trusted successful workflows already own their job selection; no second filter
  evaluator or job-name registry is needed here.
- **Auto-publish generated notes or keep mandatory checked-in per-version notes.**
  The former removes the requested human curation boundary; the latter duplicates
  GitHub notes and couples version timing to another repository edit. Keep durable
  migration docs in source and curate the verified draft instead.
- **Use a release service or automatic draft recovery.** Adds state, retries and
  broader write authority to a low-frequency operator action. Existing CLI tooling
  and explicit manual recovery are sufficient.

## Revisit when

Revisit if automatic workflows or main's job-selection trust model change, GitHub
run/attempt semantics cease to support unambiguous latest evidence, maintainers
need automatic publication or draft invalidation, or new targets/signing/recovery
requirements exceed this bounded pipeline.

## References

- [Issue #85](https://github.com/octacian/backlot/issues/85), [issue #86](https://github.com/octacian/backlot/issues/86), [original tag run](https://github.com/octacian/backlot/actions/runs/40254695599)
- [Accepted release decision](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md)
- [Release workflow](../../.github/workflows/release.yml), [release script](../../.github/scripts/release.sh), [publication guard tests](../../cmd/backlot/release_test.go)
- [CI selection and packaging checks](../testing.md), [changed paths](../../.github/workflows/changes.yml), [release procedure](../preparing-a-release.md)
- [Operations and upgrades](../operations.md), [platform policy](../platforms.md), [retained state decision](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [GitHub release creation](https://cli.github.com/manual/gh_release_create), [workflow runs API](https://docs.github.com/en/rest/actions/workflow-runs)
