# Retained v9 state

`state.db` is a closed, nonempty database produced by unmodified Backlot
`b041400c864a237b30e2d6e34435b615acd031bb` on macOS ARM64, 2026-10-22,
using Go 1.27.1. It was captured before the migration implementation changed.
It contains two actual instances: a persistent `prepared` scene and a disposable
`succeeded` native `/usr/bin/true` job, with checkout/path/persistent indexes,
snapshots, private environment values, and completed execution/cleanup history.
The native result records exit zero and `observed_empty`; the daemon stopped
normally and its retained direct-child handle joined with exit zero before copy.
This carries the native supervision policy's residual unseen-descendant risk.
No services, ports, Docker, gateway, project registry, trust or operator state were
used. HOME/XDG/PATH/TMPDIR were explicitly isolated; all values are fixture-only.

The database is 131072 bytes. SHA-256:

| Artifact | SHA-256 |
| --- | --- |
| `state.db` | `0414afad45352ac9b5678e44e3cf9373e2efbf1c5f0b025fdd7891fdaf813dc1` |
| `backlot.yaml` | `e430d3ccbd239849a037d5374d3c0ae731a9a1349dc76f45d016678468e1de2a` |
| Producer source tar (`git archive` below) | `792da52673924353419c9809b45b67ad299c4ff25a1ae4f668db7d365b852a25` |
| Producer binary | `8c547dbac3b90ecb55e73e665049ee21deee2ced862f7e79719e6b554832abfe` |

Build: `go build -trimpath -buildvcs=false -o "$capture/backlot" ./cmd/backlot`
from the exported source. `go version -m` identified Go 1.27.1, darwin/arm64,
CGO_ENABLED=1, GOARM64=v8.0, trimpath=true; dependencies are pinned by that revision's
`go.mod`/`go.sum`. No producer source changes or current-struct fixture writer.

## Regeneration workflow

Retain the original bytes. Regeneration demonstrates the pinned producer/workflow,
not byte-identical IDs, paths, timestamps, capabilities or database page layout.
Use a private short root and only the pinned binary. Run from the repository root
in a separate shell with owned native-process permission; preserve the root if
cleanup is unsuccessful or uncertain. For example, on macOS:

```sh
umask 077
capture=$(mktemp -d /tmp/blstate.XXXXXX)
mkdir "$capture/source" "$capture/project" "$capture/state" "$capture/home"
git archive b041400c864a237b30e2d6e34435b615acd031bb > "$capture/source.tar"
tar -xf "$capture/source.tar" -C "$capture/source"
(cd "$capture/source" && go build -trimpath -buildvcs=false -o "$capture/backlot" ./cmd/backlot)
cp internal/daemon/testdata/state/v9/backlot.yaml "$capture/project/backlot.yaml"
backlot_fixture() {
  env -i HOME="$capture/home" XDG_CONFIG_HOME="$capture/home/config" \
    XDG_STATE_HOME="$capture/home/state" PATH=/usr/bin:/bin:/usr/sbin:/sbin \
    TMPDIR="$capture" "$capture/backlot" "$@"
}
# Register cleanup before launch; never signal an observed PID or historical ID.
daemon_child=
cleanup_fixture() {
  if [ -n "$daemon_child" ]; then
    backlot_fixture daemon stop --state-dir "$capture/state" --json
    wait "$daemon_child"
  fi
}
trap cleanup_fixture EXIT
backlot_fixture daemon serve --state-dir "$capture/state" > "$capture/daemon.log" 2>&1 &
daemon_child=$!
# Retry only readiness observation, bounded to five seconds.
for attempt in $(seq 1 100); do
  backlot_fixture daemon status --state-dir "$capture/state" --json && break
  sleep 0.05
done
backlot_fixture daemon status --state-dir "$capture/state" --json || exit 1
backlot_fixture prepare --project "$capture/project" --state-dir "$capture/state" --json prepared > "$capture/prepared.json" || exit 1
backlot_fixture run completed --project "$capture/project" --state-dir "$capture/state" --json > "$capture/completed.json" || exit 1
backlot_fixture daemon stop --state-dir "$capture/state" --json || exit 1
wait "$daemon_child" || exit 1
daemon_child=
test ! -e "$capture/state/daemon.sock" || exit 1
go version -m "$capture/backlot"
shasum -a 256 "$capture/source.tar" "$capture/backlot" "$capture/state/state.db"
```

Inspect both JSON results: persistent status `prepared`; disposable status/outcome
`succeeded`, terminal exit zero, no infrastructure/collection/cleanup failure, and
one `observed_empty` native cleanup result. Require normal daemon join and absent
socket; a stopping acknowledgment alone is insufficient. Only then copy the closed
`$capture/state/state.db` for a new fixture, record its provenance/hashes, and remove
the owned temporary root. The capture used the equivalent commands from a private
Python subprocess owner with a `finally` stop/join; no capture subsystem ships.

`TestRetainedStateNine` opens only temporary copies, checks typed historical reads,
record bytes and reopen. Rejected-copy tests check physical byte preservation.
Never start a daemon or recovery using this fixture's embedded historical paths or
capabilities. These native-only records are not historical provider evidence.
See [the next-change procedure](../../../../../docs/testing.md#daemon-state-migrations).
