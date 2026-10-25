# Preparing a release

Follow the [release decision](adr/20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md)
and [packaging checks](testing.md#release-packaging-and-publication). Current
automation accepts only canonical stable `v0.x.y` tags, without leading zeros,
prerelease identifiers or build suffixes. It rejects `v1.x.y`; changing that policy
requires a reviewed change before tagging.

1. Finalize `docs/releases/<tag>.md` in the feature PR that introduces the release,
   or a preparation PR when needed. Describe changes, supported targets,
   compatibility and upgrade steps, known limitations, and revision-bound evidence
   where available. Link the exact tag's installation guide and schemas. Remove
   preparation-only instructions before review; do not claim publication or
   evidence that does not yet exist.
2. Review the candidate and require applicable PR CI to pass. Select additional
   manual testing only for changed guarantees whose risks lack CI coverage;
   record failures, skips and limits honestly. See [targeted diagnostics](release-acceptance/README.md).
3. Squash-merge the reviewed PR, wait for applicable required CI on the resulting
   `main` squash commit, then approve that exact full SHA for release. Notes
   finalized in the feature PR need no separate preparation PR. Do not tag a local
   candidate or an unreviewed follow-up. From the clean approved checkout, create
   and push an annotated tag at that SHA (substitute the approved tag and full commit):

   ```sh
   git tag -a v0.1.0 <approved-main-commit> -m 'Backlot v0.1.0'
   git push origin refs/tags/v0.1.0
   ```

The [release workflow](../.github/workflows/release.yml) checks that event, local
and remote tag resolve to the same commit, runs fresh `make check` on macOS ARM64
and Ubuntu amd64, and builds both GoReleaser archives without publishing. It checks
the exact archives' checksums, contents, identity, embedded attribution and private
native daemon smoke on both targets. Only after all gates pass does the write-enabled
job create, upload, verify and publish a complete draft using the reviewed notes.
It does not rebuild after smoke, create/move tags or replace assets. PR/main runs
package and smoke without publishing; they do not create a release.

An existing release (including a draft) or a failed API lookup aborts publication.
A failed upload can leave an unpublished draft: inspect it manually and explicitly
discard only that failed unpublished draft before retrying the unchanged tag.
Published tags/assets are immutable; corrections require a new patch version.
Verify public `go install github.com/octacian/backlot/cmd/backlot@<tag>` only after
actual publication. Optional repository tag/release protections are separate
operator decisions.
