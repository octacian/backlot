# Genuine closed v13 predecessor

Producer: `5e89af4b08b532d417bd136a7099d0cfac4aede1`, darwin/arm64,
Go 1.27.3, `go build -o /tmp/backlot-111-predecessor ./cmd/backlot`.
Producer SHA-256: `cf83ae69a96ec4a2d5220a9b65bc5ea9300df7c20a673e90417252c510d34940`.
Source archive SHA-256: `f484f8ffab744e6ae9fab896c9f3696664df1ecd9d83ce021019236295c0742d`.
The executable reports `+dirty` because the accepted ADR was untracked; no product
source edits existed when it was built.
Closed database SHA-256: `a51801ae7a2c64d109b88529b68a7f623fd61844dfc3824bbff271c086a0310e`.

The baseline CLI prepared one persistent scene and ran/stopped another native
scene. Explicit private machine, registry and daemon paths confined every effect.
Authenticated API shutdown joined the retained direct daemon child with exit 0
and verified socket absence before copying the database. No provider or SQL
allocation was exercised; fixture capabilities remain private.

To regenerate, extract the pinned producer with `git archive`, build with the
recorded command/toolchain, then run `capture.py` after adapting only its private
binary/evidence paths. It registers cleanup before startup and retains command,
exit and cleanup evidence outside the checkout. New IDs, paths, times and hashes
will differ. Preserve all committed predecessor bytes; tests open temporary
copies passively and never recover historical effects.
