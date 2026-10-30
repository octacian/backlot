# Genuine mixed v11 evidence

Closed database and mixed logs/artifacts captured from unmodified producer
`6efda8051396286a95903dd2189a4ce20073e1de` (Go 1.27.1, macOS ARM64).
Binary SHA-256: `6d439b261b3652ee654d6000d637438a937c39c69846f16c2bdaf000d850c337`.
Database SHA-256: `6dbccf64d77e8415d0cd762316b6252a74249b80dcbf1371251a13f80fab4725`.
The exact original v11 baseline fixture in `../v11` remains unchanged.

`capture.py` records the actual fixture-only capture: persistent job A, stop,
restart B, stop, plus a separate stopped/destroyed legacy identity, authenticated
daemon shutdown, retained direct-child join (0),
socket absence, then closed copies. The dev current v11 result is B; a second identity retains its known result under
the predecessor destroy contract. The dev mixed log
and artifact directories contain A and B. No current structs reconstructed state.
There are no providers, ports, secrets or operator settings. Raw fixture-native
capabilities remain only in this private-authority fixture database per policy;
never print/upload it in reports or recover its embedded historical paths.

To regenerate, build the pinned producer with `go build -trimpath -buildvcs=false`
from an extracted `git archive` at that revision (see `../v11/README.md`). Set
`binary` in a private copy of `capture.py` to that producer and change only its
external delivery parent if needed. Run with Python 3; the helper always creates
fresh mode-0700 HOME/config/state/registry/project/tmp paths, records commands and
keeps normal stop/join cleanup before effects. Use the committed manifest/machine
inputs unchanged. Retain fresh source/build/binary/database hashes and cleanup
receipt; IDs/times/capabilities and thus regenerated hashes legitimately differ.
Tests copy these exact bytes and operate only on copied evidence, never on the
captured runtime paths. Migration/open is passive, without recovery or actuation.
