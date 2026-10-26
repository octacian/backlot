# Retained v10 state

`state.db` is a closed, nonempty database captured from unmodified state/runtime
producer `a84b98df376195441709360be340540a87b3c45d` before #80 producer structs
changed, using Go 1.27.1 on macOS ARM64. Only the proposed ADR was untracked.
It contains the same fixture declarations as `backlot.yaml`: one persistent
prepared instance and one succeeded disposable native `/usr/bin/true` job, with
snapshots, indexes, private fixture environment and observed-empty cleanup history.
The four shared buckets exist and are empty. No Docker, trust or registry effects.

Build: `go build -trimpath -buildvcs=false -o CAPTURE/backlot ./cmd/backlot` from
the pinned revision (CGO_ENABLED=1, GOARM64=v8.0). Binary SHA-256:
`228e5b2ba9242d34b0e8f4848a4f79d4a15a82b71a133cf1458e11525b6aab9d`.
Database SHA-256:
`2753c8776228eb01cf50dbc5a3fdb398257089f44bdd4ba83ad61a894fd265ac`.
Manifest SHA-256:
`e430d3ccbd239849a037d5374d3c0ae731a9a1349dc76f45d016678468e1de2a`.

Regenerate with the [v9 capture procedure](../v9/README.md#regeneration-workflow),
substituting this producer revision and fixture. Isolate HOME/XDG/PATH/TMPDIR and
private project registry in an owned private root. Register stop/join cleanup before
launching the retained direct-child daemon; prepare `prepared`, run `completed`,
verify successful native cleanup, stop normally, join exit zero and prove socket
absence before copying the closed DB. Never recover these historical paths.
The original private subprocess capture additionally set BACKLOT_PROJECT_CONFIG_DIR
to its private home/projects. Its commands/build/settings/hashes and joined cleanup
receipt are retained externally in the #80 delivery ledger. Temporary capture
state was removed after the closed copy; no fixture runtime remains.

Tests copy these genuine bytes and exercise migration/read/reopen without provider
effects, alongside the oldest v9 fixture. Historical bytes are not regenerated
using current structs. See [state migration policy](../../../../../docs/testing.md#daemon-state-migrations).
