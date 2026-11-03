# Genuine closed v12 predecessor

Captured from unmodified producer `11fb43f01ff720b83bcba82c50226689eb17775c`
before runtime edits, using Go 1.27.1, darwin/arm64, CGO enabled and
`go build -trimpath -buildvcs=false ./cmd/backlot`. Source archive, binary,
commands, hashes, private runtime diagnostics and cleanup receipt are retained at
`/tmp/backlot-filesystem-reassociation-delivery/predecessor-v12`.

The real CLI prepared one persistent scene and ran/stopped another native scene
using `/bin/sh`, explicit private machine configuration, project registry and daemon
state. It used no ports/providers or generated application credentials. The
permissioned database contains fixture-only native cleanup capabilities; do not
print/export raw state or recover its embedded historical paths.

The daemon was stopped through its API, its retained direct child joined with exit
0 and socket absence verified before copying the closed database. SHA-256:
`f273744143a951f540e1924e9d28d56b94e2545391b74c2ee67a5ed13315ffff`.

To regenerate, extract `git archive` of that pinned producer to a private directory,
build the binary there with the recorded flags/toolchain, and use a private copy of
`capture.py` with only its evidence directory/binary paths adapted. The helper makes
fresh owned runtime paths, retains cleanup before effects, asserts state12, and
records exact commands, exits and hashes. Regenerated IDs/times/capabilities and DB
hashes legitimately differ. The committed manifest/machine inputs are the capture
inputs, never reconstructed current structs. Preserve original fixture bytes.

Tests copy the database to temporary state and migrate/open/reopen passively; they
never run recovery against historical runtime. The capture proves native v12
records, not historical Docker, SQL, gateway or other-platform compatibility. Follow
[retained migration rules](../../../../../docs/testing.md#daemon-state-migrations).

## Nonempty shared registration

`state-shared.db` supplements the unchanged native capture with two real prepared
persistent scenes and a registered shared project/path, produced by the same frozen
unmodified v12 binary. This supplemental capture ran after implementation edits;
the predecessor executable and original native fixture stayed unchanged. Preparation
used an explicit unavailable fixture Docker endpoint and never provisioned/contacted
a provider. It generated no SQL credentials or allocations. The owned daemon joined
with exit 0 and socket absence before copying the closed database.

SHA-256: `242328c99016722a6ac633450c46ea182d644003223be396b44658f86f6f0cb1`.
Producer executable SHA-256:
`e8dc819583012450ead8e007eda60bbad336005ce583449130af85f6fda0787d`.
Run `capture.py --shared-registration` using that v12 binary to regenerate into a
fresh private evidence child. The helper contains the exact extra manifest/machine
inputs; commands, inputs, cleanup receipt and hashes are retained at
`/tmp/backlot-filesystem-reassociation-delivery/predecessor-v12/shared-registration.4n2d_yt4`.
Tests migrate a copy without recovery; this proves nonempty shared-identity migration,
not historical provider execution. The original native capture's source archive
SHA-256 is `2d0638e279770c7984e878712d15479f8cbc2bc14fe8482fe4ad18295e7df932`.
