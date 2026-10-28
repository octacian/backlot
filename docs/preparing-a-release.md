# Preparing a release

Follow the [release decision](adr/20261026T020229927Z-reuse-exact-main-ci-and-prepare-verified-release-drafts.md)
and [packaging checks](testing.md#release-packaging-and-publication).

1. Review the candidate and require applicable PR CI to pass. Keep compatibility
   and migration instructions in durable repository docs such as
   [operations and upgrades](operations.md) and [state compatibility](containers.md#state-compatibility).
   Select [targeted diagnostics](release-acceptance/README.md) for changed guarantees
   lacking CI coverage; record failures, skips and limits. Per-version checked-in
   notes are no longer required; retain historical notes.
2. Squash-merge and wait for all five workflows on the resulting exact full `main`
   SHA. Each workflow's newest main-push run and latest attempt must succeed:

   | Workflow | Path under `.github/workflows/` |
   | --- | --- |
   | Check | `check.yml` |
   | Docs | `docs.yml` |
   | Application scenes | `scenes.yml` |
   | Current-head project inputs | `project-inputs.yml` |
   | Release archives | `release.yml` |

   Legitimate job-level path skips within successful workflows are allowed.
   Missing, unknown, pending, failed, cancelled or skipped workflow evidence blocks
   drafting; PR, manual and other-SHA runs do not qualify.
3. Approve that exact main commit. From its clean checkout, create and push an
   annotated canonical stable `v0.x.y` tag (no leading zeros, prerelease or build
   suffix; `v1.x.y` requires a policy change). Substitute the approved tag and SHA:

   ```sh
   git tag -a v0.1.1 <approved-main-commit> -m 'Backlot v0.1.1'
   git push origin refs/tags/v0.1.1
   ```

   Preserve the existing `v0.1.0` tag. Rerunning it uses its old workflow definition;
   the fixes require newly merged source and a new tag.
4. Wait for the [release workflow](../.github/workflows/release.yml) to verify tag
   identity and main CI, freshly package and smoke both native archives, recheck
   main CI, then upload and verify an unpublished draft. See the
   [packaging checks](testing.md#release-packaging-and-publication) for coverage.
5. Inspect the successful workflow and curate the draft's generated notes: changes,
   targets, compatibility/migration links, known issues and evidence limits. Check
   its tagged installation/upgrade, migration and schema links. GitHub uses the
   ordinary previous-release baseline; the first actual release may include earlier
   unreleased work. Confirm the mutable draft still contains the verified assets,
   then explicitly publish in GitHub. CI is checked at drafting time, not continuously.
   Verify public `go install github.com/octacian/backlot/cmd/backlot@<tag>` after
   publication.

## Recovery

If preflight or the pre-draft recheck blocks, inspect its workflow/run/attempt
message, complete or rerun main CI for that SHA, then rerun the tag workflow.
API errors and incomplete evidence fail closed; restore API access or resolve the
missing evidence before retrying.

An existing release, including a draft, aborts drafting. A failed upload or
verification may leave an unpublished partial draft: inspect it and explicitly
discard only that failed unpublished draft before retrying the unchanged tag.
Never delete a published release for recovery. Published tags/assets are immutable;
corrections require a new patch version. Optional repository tag/release protections
remain separate operator decisions.

## Installed agent skills

Before release, run `make skills-check` and verify the skill procedures against the
release CLI identity, required capabilities and targeted help. Update canonical
guidance and regenerate bundled material with command changes; test installation
from the selected published source when available. Keep Node optional and preserve
the existing binary archive contents. Link [skill installation/update guidance](agent-skills.md)
from release notes when the skill workflow changes.
