# Testing and tooling

## Setup

Use the Go version declared in `go.mod` (currently 1.27.1), Git, and Make.
Repository tooling is Go-only; Node and a JavaScript package manager are not
required. Download module dependencies with `go mod download`.

`make tools` installs the pinned golangci-lint version into `.tools/bin` using
the current Go toolchain. This first build can take several minutes. It does
not change global tooling. Lint configuration lives in `.golangci.yml`.

## Commands

Run these from the repository root:

| Command | Purpose |
| --- | --- |
| `make build` | Build `bin/backlot` |
| `bin/backlot daemon start --state-dir /absolute/private/path --json` | Start an explicit local metadata daemon after building |
| `bin/backlot doctor --state-dir /absolute/private/path --json` | Check local daemon access; provider checks deferred |
| `go run ./cmd/backlot version --json` | Inspect the scaffold's typed version result |
| `go run ./cmd/backlot plan test --project examples/adoption --json -- -run TestExample` | Validate an offline fixture plan and forward terminal arguments |
| `make fmt` | Format Go source |
| `make fmt-check` | Check Go formatting without editing |
| `make vet` | Run `go vet ./...` |
| `make test` | Run Go tests |
| `make race` | Run Go tests with the race detector |
| `make tools` | Install pinned development tooling locally |
| `make lint` | Validate lint configuration and run golangci-lint |
| `make adr-create TITLE="Decision title"` | Create a proposed ADR without overwriting records |
| `make adr-verify` | Verify all ADR records |
| `make adr-test` | Run ADR tooling tests |
| `make schema-generate` | Regenerate committed editor schemas from `api/v1` |
| `make schema-check` | Reject stale generated schemas without writing |
| `make check` | Run schema drift checks, formatting, vet, lint, race tests, ADR verification, and build |

Before committing scaffold or Go/tooling changes, run `make check`. Documentation
changes require ADR verification when records change and manual checking of
links, commands, and consistency with the product contract. Run additional
focused checks when changed behavior warrants them; do not add tests that merely
duplicate implementation details.

## CI and platform evidence

GitHub Actions runs the shared checks on macOS ARM64. Linux checks provide early
portability evidence, not a Linux runtime support claim. A Windows cross-build
checks compilation only, not process lifecycle, networking, or installation.
The workflow runs on every PR and push to `main`, without path filters that
could leave required checks missing.

Schema tests use a maintained JSON Schema validator on the real YAML examples
and negative/variant cases, in addition to checking deterministic generated output.
See [schema generation and editor setup](../schemas/README.md).

Tests exercise strict manifest/config decoding, graph/reference validity, offline
CLI JSON/errors, environment precedence, read-only seeds, secret redaction, tool
availability and ADR validation. Daemon tests additionally exercise real temporary Git worktrees and aliases,
concurrent preparation, typed Unix-socket contracts, private snapshots, leases,
shutdown, owned-child crashes and intent/effect recovery. See the
[daemon guide](daemon.md). Native integration tests additionally build the real CLI, launch private fixture
daemons and native descendants, and exercise readiness, cancellation, retained
logs, leases, compatible restart and crash/uncertain-ownership recovery. They do
not demonstrate Docker integration, HTTPS publication, or all V1 workflows.
The [implementation plan](implementation-plan.md) defines those later acceptance
checks. Runtime integration tests must use owned resources and must never modify
an operator's unrelated containers, routes, files, or databases.

The race gate disables Go test result caching with `-count=1`. Native integration
builds and executes a separate CLI/guardian binary; changes to that subprocess's
private helper code may leave the parent test binary unchanged, so cached results
cannot validate the current runtime. `make check` and CI execute this suite fresh.

Crash fixtures must retain cleanup before fault injection. Signals may target
unreaped direct children through their retained process handles, or an
individually authenticated live fixture may signal itself or its own supervised
group. Observed PIDs/PGIDs, including values reconstructed from PID files or ps,
never authorize actuation. The private guardian self-fault hook is disabled by
default and enabled only for explicit owned fixture daemon/guardian launches;
workload activation environment cannot enable it. Full receiver identity and
capability checks precede self-fault. Capability data stays in private temporary
state and is excluded from reports. Failure paths join owners and prove absence,
or preserve diagnostic state and report residuals.

## Docker/private-state integration

Provider tests are explicit opt-in and require a local Docker Unix socket plus
pre-pulled `mariadb:11.4` and `alpine:3.21` images. Backlot itself never pulls/builds
images. Run this after the provider-independent `make check` gate:

```sh
docker pull mariadb:11.4
docker pull alpine:3.21
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestDockerRuntime$' -count=1 -v
```

The suite builds the real race-instrumented CLI/daemon and uses uniquely owned
containers, networks, volumes, private directories and loopback ports. It exercises
MariaDB persistence/reset through CLI/daemon/Docker; drift rejection before stop;
failed/successful fresh-only jobs; each-start jobs; real concurrent worktrees and
disposable runs; output serialization; partial allocation and interrupted cleanup;
owned daemon crash recovery; uncertain ownership; foreign consumer blocking and
unrelated preservation. Faults use retained direct-child daemon handles, never
observed PIDs. Cleanup is registered before faults and preserves uncertain residuals
for diagnosis. Docker tests skip only when the explicit endpoint variable is absent;
a configured but unavailable provider is a failure. The complete suite must pass on
the frozen feature candidate after independent reviews; `make check` alone does not
prove Docker integration. Logs and exact source/image/config provenance belong in
the feature evidence ledger.

## HTTPS/mixed integration

Use an explicit native Caddy binary and local Docker endpoint; both suites skip
only when their opt-in settings are absent. Pre-pull the pinned gateway fixture
image outside Backlot runtime (there are no implicit image pulls):

```sh
docker pull caddy:2.11.6
docker pull alpine:3.21
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/gateway -count=1 -v
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestHTTPSRuntime$' -count=1 -v
```

Run suites serially with exclusive fixture mutation ownership. Gateway fixtures
use private cert/config/storage, dynamic ports, exact owned route effects and
retained child handles or labeled containers. The container gateway is pinned to
the Caddy 2.11.6 multi-platform digest
`sha256:3422ce6de165df66534f9b9ba50efaf457114ec961763cc52f5dbdaac2972d73`.
Each suite builds a unique image from the generic static Go fixture source and
pre-pulled `alpine:3.21` (including standard public CA roots);
cleanup verifies ownership and removes fixture images/containers/networks and
private state, preserving uncertain residuals. Shared pre-pulled images remain.
No fixture uses the operator's gateway ports 443/2019 or installs global trust.

The gateway suite covers real native/container Caddy, TLS-verified host/container
consumers, native API/container SSR, literal routing, WebSockets, redirects/cookies,
ambiguous mutation response recovery and preservation. The daemon suite adds real
daemon/guardian execution, normal host `.localhost` DNS, origin-dependent terminal
container clients, explicit fixture-only container dialing with TLS hostname
verification, mixed service references, stop/start/restart/reset/destroy and crash
recovery. These development fixtures do not prove real operator DNS/certificate
setup. Frozen-candidate acceptance also requires the complete Docker suite and
real owned-domain trusted HTTPS from macOS ARM64 host, browser and containers,
including SSR/API/WebSockets and relevant redirects/cookies. Record commands,
candidate revision, fixtures/config/image provenance, logs, skips/failures and
cleanup in the delivery ledger; never substitute mock success for missing evidence.
