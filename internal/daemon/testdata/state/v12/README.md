# Genuine closed v12 predecessors

Producer: `11fb43f01ff720b83bcba82c50226689eb17775c`, Go 1.27.1, darwin/arm64,
CGO enabled, `go build -trimpath -buildvcs=false ./cmd/backlot`.
Producer executable SHA-256:
`e8dc819583012450ead8e007eda60bbad336005ce583449130af85f6fda0787d`.
Source archive SHA-256:
`2d0638e279770c7984e878712d15479f8cbc2bc14fe8482fe4ad18295e7df932`.

| Closed fixture | Contents | SHA-256 |
| --- | --- | --- |
| `state.db` | One prepared persistent scene; another native scene run/stopped with `/bin/sh` | `f273744143a951f540e1924e9d28d56b94e2545391b74c2ee67a5ed13315ffff` |
| `state-shared.db` | Two prepared persistent scenes and nonempty shared project/path registration; no allocations or SQL credentials | `242328c99016722a6ac633450c46ea182d644003223be396b44658f86f6f0cb1` |

Both use explicit private machine configuration, registry and daemon state; shared
preparation selects an unavailable fixture endpoint and never contacts a provider.
The daemon was API-stopped, its retained direct child joined with exit0, and socket
absence verified before copying. This proves native/prepared metadata, not historical
Docker, SQL, gateway or other-platform execution. Fixture-only cleanup capabilities
remain private: never print/export raw state or recover embedded historical paths.

To regenerate, extract `git archive` of the pinned producer to a private directory
and build with the recorded toolchain/flags. Copy `capture.py`, adapting only its
private evidence directory/binary paths; run `python3 capture.py` for native state
or `python3 capture.py --shared-registration` for the supplemental shared state.
The helper owns fresh paths, installs cleanup before effects, asserts state12 and
records commands, exits, inputs and hashes. Committed manifest/machine files and
the helper's shared variant are the capture inputs. IDs/times/capabilities and new
DB hashes legitimately differ; preserve the committed predecessor bytes.

Tests migrate/open/reopen temporary copies passively, never historical recovery;
follow [migration rules](../../../../../docs/testing.md#daemon-state-migrations).
