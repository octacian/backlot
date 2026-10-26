# Preparing a release

Follow the [release decision](adr/20261026T020229927Z-reuse-exact-main-ci-and-prepare-verified-release-drafts.md)
and [packaging checks](testing.md#release-packaging-and-publication). Automation
accepts only canonical stable `v0.x.y` tags without leading zeros, prerelease
identifiers or build suffixes. It rejects `v1.x.y`; changing that policy requires
a reviewed change before tagging.

1. Review the candidate and require applicable PR CI to pass. Keep compatibility
   and migration instructions in durable repository documentation such as
   [operations and upgrades](operations.md) and [state compatibility](containers.md#state-compatibility).
   Select additional manual testing only for changed guarantees whose risks lack
   CI coverage; record failures, skips and limits honestly. See
   [targeted diagnostics](release-acceptance/README.md). Per-version checked-in
   notes are no longer required; existing historical notes remain available.
2. Squash-merge the reviewed PR and wait for all five automatic workflows on the
   resulting exact full `main` SHA: Check, Docs, Application scenes, Current-head
   project inputs and Release archives. Each workflow's newest main-push run and
   latest attempt must complete successfully. Existing job-level changed-path
   skips are allowed within successful workflows; missing, unknown, pending,
   failed, cancelled or skipped workflow evidence blocks drafting. PR checks,
   manual runs and another SHA cannot satisfy this gate.
3. Approve that exact main commit. From the clean approved checkout, create and
   push an annotated tag (substitute the approved tag and full commit):

   ```sh
   git tag -a v0.1.1 <approved-main-commit> -m 'Backlot v0.1.1'
   git push origin refs/tags/v0.1.1
   ```

   Leave the existing `v0.1.0` tag unchanged. An old tag rerun uses its old workflow
   definition; these fixes arrive through the newly merged source and new tag.

The [release workflow](../.github/workflows/release.yml) checks event/local/remote
tag identity and exact-SHA trusted main CI instead of repeating repository tests.
If preflight blocks, inspect its workflow/run/attempt diagnostic, complete or rerun
main CI for that SHA, then rerun the tag workflow. It fails closed on API errors
and incomplete evidence; no override is provided.

After preflight, the workflow freshly builds both GoReleaser archives without
publication, checks their checksums, contents, identity, embedded attribution and
private native daemon smoke on macOS ARM64 and Ubuntu amd64, then rechecks main CI
before creating a draft. It generates GitHub release notes with tagged links to
installation/upgrade, state compatibility and schemas, uploads only the smoked
assets, verifies the complete set and downloaded bytes, and leaves the verified
release **unpublished**. It never rebuilds after smoke, creates/moves tags or
replaces assets. PR/main runs package and smoke when relevant and never draft.

4. Inspect the successful workflow and its verified draft. Curate GitHub's
   generated notes: check changes, supported targets, compatibility and migration
   links, known issues and evidence limits. GitHub chooses the ordinary previous
   release baseline; the first actual release may include earlier unreleased
   work. Confirm the draft still contains the verified assets, then explicitly
   publish it in GitHub. CI evidence is checked at drafting time, not continuously
   locked against future reruns or draft edits.

An existing release, including a draft, or a failed API lookup aborts drafting.
A failed upload or verification can leave an unpublished partial draft: inspect it
manually and explicitly discard only that failed unpublished draft before retrying
the unchanged tag. Never delete a published release for recovery. Published
tags/assets are immutable; corrections require a new patch version. Verify public
`go install github.com/octacian/backlot/cmd/backlot@<tag>` only after actual
publication. Optional repository tag/release protections are separate operator
decisions.
