# Genuine closed v11 predecessor capture

Captured 2026-10-29 UTC on macOS ARM64 from unmodified pinned producer
`6efda8051396286a95903dd2189a4ce20073e1de`, before any format edit.
All capture files, build caches/modules, source, binary, machine configuration,
registry, runtime state, reports and provenance are under this private directory:
`/tmp/backlot-99-delivery/predecessor-v11`.
No checkout file was edited and no commit, publication or provider mutation occurred.

## Fixture to retain

`fixture/state.db` is a CLOSED 262144-byte bbolt database, mode 0600 beneath a
0700 root. It was copied only after authenticated daemon stop, normal direct-child
join with exit 0 and proven absence of `state/daemon.sock`. It contains real CLI/
client/daemon-produced records, not records reconstructed with current Go structs.

Read-only bbolt inspection confirms state version 11 and exactly three instances:

| Scene | Lifetime / state | Observed original exit | Evidence |
| --- | --- | --- | --- |
| prepared | persistent / prepared | None, execution never accepted | Real private snapshot/index records |
| completed | disposable / succeeded | 0 | stdout/stderr, one artifact, complete audited export |
| failed | disposable / failed | 17 | stdout/stderr, one artifact, complete audited export |

Both executed jobs reported observed-empty native cleanup, completion timestamps,
no collection or cleanup failure and no kept runtime. Exact public attempt identity
was checked between final result, inspect, selected log page and portable export.
The failed CLI returned the original 17, not a fabricated infrastructure exit.
The source fixture uses only `/bin/sh`, fixture strings, output files and no shared
services, ports, Docker, Caddy, trust, network services or generated credentials.
Private runtime capabilities inside the DB are fixture-only; do not print/upload
raw database or raw responses. The fixtures include current known execution results;
there is no invented overwritten-attempt history.

The DB has 3 snapshots, 3 private secret records, 2 runtime journals, 2 resource
records, 1 persistent index and empty shared-service namespaces. Known launch
provenance was produced naturally by the baseline. This is a non-Git fixture
project: unavailable Git-head provenance remains absent rather than invented.

## Hashes

| Artifact | SHA-256 |
| --- | --- |
| `fixture/state.db` | `6ca1ade1b29836ad6ab9f95f005b9fec8eee968c4a0adfefbf1cd60e7417787d` |
| `fixture/backlot.yaml` | `62072f17d25fdf92ba5a7da9dfc68572fa674da6d2c49f3cf4db9d4e7bc6c45d` |
| `fixture/machine.yaml` | `56529163b7ab6a83a6aadc8438f48b85491c2a535e95ff70766ded3661d22d4f` |
| `source.tar` | `ecbddc3231102e15654b31397b8030a9a91aa10b59ea87ec9c7f6e56358eea1e` |
| `backlot` | `6d439b261b3652ee654d6000d637438a937c39c69846f16c2bdaf000d850c337` |
| `capture.py` | `a6c8dbc8874d325aca44ab94d6e282f2ec81a7a010db10993c24a29417a69229` |

`hashes.json` includes byte sizes. Read-only inspection did not change DB hash;
all 581 regular extracted source files still exactly match the pinned archive.
No producer patch, current-struct writer or test-only producer seam was used.

## Build and exact command evidence

Producer archive:

```sh
git archive 6efda8051396286a95903dd2189a4ce20073e1de > /tmp/backlot-99-delivery/predecessor-v11/source.tar
```

Build in the extracted `source/` with Go 1.27.1, darwin/arm64,
`CGO_ENABLED=1`, `GOARM64=v8.0`, `GOTOOLCHAIN=local`:

```sh
/usr/local/go/bin/go build -trimpath -buildvcs=false -o /tmp/backlot-99-delivery/predecessor-v11/backlot ./cmd/backlot
```

`build-command.json` records exact argv, cwd and isolated build environment;
`build-provenance.txt` records `go version`, `go version -m` and target settings.
`build.log` records dependency acquisition/build output. All dependency versions
come from the pinned source's unchanged go.mod/go.sum, with caches under this root.

`capture.py` is the exact capture program executed. `commands.json` contains
actual lifecycle/inspection/log/export argv, cwd and command exits; `runtime-env.json`
contains the exact intentionally minimal environment. Every configuration-composing
prepare/run command had explicit `--config machine.yaml`, explicit project and
state paths. HOME, XDG_CONFIG_HOME, XDG_STATE_HOME, TMPDIR, PATH and
BACKLOT_PROJECT_CONFIG_DIR were explicitly isolated. The registry remained empty;
no operator machine defaults, services or project associations were changed.

The runtime sequence was:

1. Retain a directly owned `daemon serve` child and register `finally` teardown.
2. Bounded status-only readiness observation; assert reported state format 11.
3. Prepare `prepared`; run `completed` expecting exit 0; run `failed` expecting 17.
4. For each completed run, assert original results/cleanup and artifact presence,
   read exact-attempt log page, export a complete audited bundle, and inspect identity.
5. Authenticated `daemon stop`; join the retained direct-child owner normally;
   assert exit 0 and absent socket, then copy closed state into `fixture/state.db`.
6. Open only the copy read-only with `inspect-db.go`, check version/bucket/state
   counts, confirm unchanged DB hash and unchanged extracted producer files.

For regeneration, preserve these original bytes. Repeating the workflow creates
new legitimate IDs, times, capabilities and paths; hashes of generated DB/binary
may differ with environment and must be recorded independently. The following
uses a fresh child of this capture root and changes only the capture helper's
output root in memory, never the archived producer:

```sh
python3 - <<'PY'
import pathlib, tempfile
original = pathlib.Path('/tmp/backlot-99-delivery/predecessor-v11')
fresh = pathlib.Path(tempfile.mkdtemp(prefix='regenerated.', dir=original))
program = (original / 'capture.py').read_text()
program = program.replace("root = pathlib.Path('/tmp/backlot-99-delivery/predecessor-v11')", "root = pathlib.Path(" + repr(str(fresh)) + ")", 1)
# Retain the exact regeneration helper too, since capture records its hash.
(fresh / 'capture.py').write_text(program)
exec(compile(program, str(fresh / 'capture.py'), 'exec'))
PY
```

This requires the pinned revision available to `git archive` and Go 1.27.1 at the
recorded path. If regenerating elsewhere, adapt only the capture's repository/root
and Go executable paths, retain exact commands/settings/hashes, and use exclusively
private fixture roots. Do not run `capture.py` directly against the existing
original output root: it intentionally rejects replacement exports.

## Cleanup and limits

`cleanup.json` proves stop requested, daemon joined, exit 0 and socket absent.
There was no forced kill and no failed runtime cleanup. Authenticated native
cleanup reported observed empty for both attempts; that preserves the accepted
residual risk of unseen native descendants rather than claiming universal absence.
No fixture application process is intentionally retained. Source, caches, closed
DB, private raw state and exports are retained deliberately as requested evidence.

`summary.json`, `db-inspection.json`, `source-verification.json` and `hashes.json`
are safe bounded summaries. `raw/` and `state/` are private diagnostics and must
remain private; `exports/` contains only the audited fixture-only published bundles.
Do not start a daemon or recovery against the captured fixture's embedded historical
paths/capabilities. Future tests copy DB bytes to temporary paths and open/migrate
without actuating historical runtime. Migration tests should retain the v9/v10
fixtures and this genuine v11 predecessor, following the cumulative policy.

Skipped: production changes, committed-fixture installation, full make check,
provider tests, independent review and migration execution; none is part of this
capture-only dispatch. This native macOS predecessor does not prove historical
Docker/gateway/shared-service behavior or another platform's runtime compatibility.
