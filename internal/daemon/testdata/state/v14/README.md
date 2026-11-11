# Genuine closed v14 predecessor

Producer: `6178482084f076fc71efadf24e41091db1a42cb6`, darwin/arm64,
Go 1.27.3, `go build -o /tmp/backlot-125-128/backlot-v14-predecessor ./cmd/backlot`.
The repository was clean and product sources unmodified when built.
Producer binary SHA-256: `250cdd86935ee2aa4397877619e6b1ee03d39102bdf65b28de1b44ea50db2d58`.
Closed database SHA-256: `a7006f3d252791751d304f91e01a5797e905e85eafb893029a259a07ed6d62d0`.

The existing v13 capture procedure was adapted to this producer/version. It prepared
one persistent scene and ran/stopped a native job in another. No Docker, shared SQL
or dependent task effects were captured. All paths, registry and daemon state were
private fixture-owned directories. Authenticated shutdown joined the retained daemon
child with exit 0 and verified socket absence before copying the closed database.
The owned runtime directory was then removed and absence checked.

To regenerate, build the pinned unmodified producer and run `capture.py`, adapting
only its binary/evidence paths. The script retains cleanup before starting the
daemon and records commands and hashes outside the tree. New IDs, paths and hashes
will differ. Preserve historical bytes; tests open passive temporary copies and
never recover the recorded effects. Raw capture evidence is outside the repository
under `/tmp/backlot-125-128/evidence/v14` for this implementation session.
