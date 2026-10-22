# Testing and tooling

## Setup

Use the Go version declared in `go.mod` (currently 1.27.1), Git, and Make.
Repository contributor tooling is Go-only; Node and a JavaScript package manager
are not required by `make check`. The optional [application CI example](../examples/ci-scenes/README.md)
pins its own Node/Playwright dependencies and runs in a separate Ubuntu workflow.
Download module dependencies with `go mod download`.

`make tools` installs the pinned golangci-lint version into `.tools/bin` using
the current Go toolchain. This first build can take several minutes. It does
not change global tooling. Lint configuration lives in `.golangci.yml`.

## Commands

Run these from the repository root:

| Command | Purpose |
| --- | --- |
| `make build` | Build `bin/backlot` |
| `make release-build` | Reproducible binary (defaults macOS ARM64; explicit RELEASE_GOOS/RELEASE_GOARCH for Linux) |
| `make notices`, `make notices-check` | Generate/verify linked dependency license notices |
| `bin/backlot daemon start --state-dir /absolute/private/path --json` | Start an explicit local metadata daemon after building |
| `bin/backlot doctor --state-dir /absolute/private/path --json` | Check local daemon access and paths; select scene/instance for providers |
| `bin/backlot scene start --json dev` | Start a persistent scene in the current checkout and wait for readiness |
| `bin/backlot scene status --follow --duration 30m --json dev` | Observe bounded sampled persistent-scene snapshots without lifecycle ownership |
| `bin/backlot status --follow --json INSTANCE_ID` | Observe one persistent, disposable or historical ID without checkout discovery |
| `bin/backlot scene stop --json dev` | Stop a recorded scene even with a broken or deleted manifest |
| `go run ./cmd/backlot version --json` | Inspect the typed version result |
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
| `make check-implementation` | Run schema/notice drift checks, formatting, vet, lint, Go race tests, and build |
| `make check-docs` | Run ADR tooling tests and verify ADR records |
| `make check` | Run both implementation and documentation checks |

Before committing scaffold or Go/tooling changes, run `make check`. Documentation
changes require ADR verification when records change and manual checking of
links, commands, and consistency with the product contract. Run additional
focused checks when changed behavior warrants them; do not add tests that merely
duplicate implementation details.

## CI and platform evidence

GitHub Actions runs shared checks on macOS ARM64 and Ubuntu 24.04 amd64. A separate
Ubuntu runtime job installs a reproducible Linux binary and runs real native,
Docker, Caddy and disposable-browser gates. Windows cross-build CI is removed;
native Windows runtime CI is planned in #37/#38 and is not implemented here.
The [Windows acceptance matrix](windows-design.md#epic-acceptance-and-evidence-matrix)
separates future runtime CI from required Windows 11 desktop adoption evidence.
Debian 13.4 amd64 requires actual-host runtime evidence; WSL is configuration guidance only. See [platform policy](platforms.md).
Workflows start on every PR and push to `main` (the independent consumer
is manual-only). A shared
[changed-paths workflow](../.github/workflows/changes.yml) uses pinned
[dorny/paths-filter](https://github.com/dorny/paths-filter) to skip irrelevant jobs.
Selection happens at job level; workflow-level path filters can leave required
checks pending. Detection failures run the gates rather than
silently skipping validation. PR detection uses Git to compare the checked-out
merge commit with the event's base SHA, including fork PRs, avoiding the PR files
API's 3,000-file limit. Main pushes compare with the pre-push commit, including
multi-commit pushes.

| Changed files | Jobs that run |
| --- | --- |
| `docs/` files or Markdown anywhere, including ADR additions/deletions | Docs workflow: ADR tooling tests and record verification |
| Go/runtime sources, schemas, non-CI examples, module/build/lint configuration | macOS/Ubuntu repository checks, Linux runtime, current-head scenes and project inputs |
| `THIRD_PARTY_NOTICES.md` | Repository and Linux installation checks, plus Docs |
| `actions/` implementation | Current-head scenes and project inputs |
| `examples/ci-scenes/` implementation | Scenes |
| `examples/ci-inputs/` implementation | Project inputs |
| An automatic workflow | Its own gates; shared change-detection edits run all automatic gates |

Markdown inside implementation/example directories stays documentation-only.
The [Docs workflow](../.github/workflows/docs.yml) always owns documentation
validation: it runs `make check-docs` whenever documentation or its tooling,
module dependencies or Makefile change. Mixed implementation/documentation PRs
run both workflows independently. The Check workflow runs
`make check-implementation` and does not validate ADR records. Its Go test suite
still tests ADR tooling code as part of the complete Go suite. The independent consumer is a manual
[current-commit packaging smoke test](ci.md#independent-action-consumer), without
browser shards or repeated lifecycle checks. It does not use path detection.
Go package tests remain together: shared packages and separately built subprocesses
make narrower package selection unreliable. Keep new executable inputs covered
by the filters when adding directories or build configuration. Local `make check`
continues to run the complete contributor gate.

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
the revision-bound external evidence described in the
[release verification guide](release-acceptance/README.md).

Focused milestone 6 provider coverage (run serially with the same resource grant):

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestDisposableEvidenceDocker$' -count=1 -v
```

This exercises container artifacts before removal, historical logs after destroy,
exact job results, kept failure/cancellation/collection errors and healthy services,
and injected directory-ownership cleanup failure with explicit recovery. It is
required alongside the complete Docker runtime suite on the frozen candidate.
The provider-independent `TestDisposableEvidenceNative` runs in `make check` and
covers native artifacts, fixture metadata/secrets, kept cancellation and client
loss, shutdown/crash retained data, and executable SIGINT/SIGTERM exit codes.

## HTTPS/mixed integration

Use an explicit native Caddy binary and local Docker endpoint; both suites skip
only when their opt-in settings are absent. Pre-pull the pinned gateway fixture
image outside Backlot runtime (there are no implicit image pulls):

```sh
go test -race ./internal/gateway/testdata/fixture -count=1 -v
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
The fixture uses Coder's WebSocket implementation for protocol validation and
buffered upgrades. Contract tests reject fixed/corrupted echoes, require actual
variable-payload echoes and reject malformed frames; they also run through the
gateway package during `make check`.

The gateway suite also inspects actual managed Caddy socket families: an explicit
IPv4 wildcard must be TCP4, IPv6 must remain loopback, and admin must remain
IPv4 loopback. Generated address strings alone cannot prove exposure. Saved
omitted-bind loopback configuration remains compatible; unsafe generic-wildcard
saved configuration is rejected without repair.

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
cleanup in revision-bound external evidence; follow the
[release verification guide](release-acceptance/README.md) and never substitute
mock success for missing evidence.

## Managed local-CA gateway integration

Run the native managed suite with the same exclusive owned fixture grant:

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
  go test -race ./internal/daemon -run '^(TestManagedHTTPSRuntime|TestManagedForceRestart)$' -count=1 -v
```

This is required on the frozen candidate alongside the complete gateway,
HTTPS runtime, Docker runtime and disposable evidence suites above. It builds
race-enabled CLI/native fixtures, uses dynamic IPv4/IPv6 loopback ports and a
unique private daemon/CA state, and runs real CLI lifecycle and path traffic with
normal host DNS. Private trust is explicit only in fixture clients/daemon process
(`SSL_CERT_FILE` with Go 1.27.1 and an empty private `SSL_CERT_DIR`, plus fixture
client root pools); production does not inject CA trust. Missing/wrong private
trust must fail. Tests register authenticated cleanup before faults and preserve
uncertain residuals. No existing gateway, trust store or resolver is changed.

The suite also maps the managed origin's `ca_cert_path` through explicit native
assignments and an included reusable environment set. Trusted clients succeed;
missing/wrong explicit trust fails with a TLS authority diagnostic. To additionally
execute the Node mapping and canonical custom bootstrap in [application trust](https.md#explicit-application-trust),
set `BACKLOT_NODE_TEST_BINARY` to an explicit absolute Node executable (Node 26 was
verified). This optional check also verifies that an explicit wrong `ca` overrides
`NODE_EXTRA_CA_CERTS`; Node is not required by contributor gates.

These tests do not prove standard macOS/browser adoption setup. That gate requires
an isolated disposable macOS environment, stock separate `caddy trust`, normal
hostname resolution and a browser that accepts the certificate without a warning.
Record it as blocked when isolated trust cannot be demonstrated; a TLS bypass,
resolver override or browser proceed-anyway is never evidence. External mode's
real-domain/container acceptance remains its own contract. Full release/epic
walkthroughs remain outside this implementation slice.

## Managed container trust integration

Use the same exclusive serial fixture grant, pre-pulled `alpine:3.21`, native
Caddy and explicit local Docker socket. This suite targets macOS ARM64 Docker
Desktop bridges and rootful Linux IPv4 bridges. The Linux fixture explicitly sets
`https_bind_address: 0.0.0.0` on dynamic HTTPS ports; admin stays loopback:

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestContainerManagedHTTPSRuntime$' -count=1 -v
```

The real daemon suite builds the [shipped Go application](../examples/container-https/README.md)
and a unique image from the pre-pulled Alpine base. It verifies explicit Engine
`host-gateway` canonical mappings and readonly public-root file mounts, correct/
missing/wrong trust with ordinary Go root loading and DNS, native frontend/API
with container SSR/test, variable WebSockets, redirects and secure cookies. A
private rejecting socket verifies zero Docker contacts for the native-only peer
despite an unselected container declaration. Actual mount/mapping inspection,
unsafe drift fencing, peer isolation, fresh creates across stop/start/restart/
reset, volume retention/reset, destroy replacement and container/route journal
intent-effect crash recovery preserve the existing ownership contracts.

Docker Desktop may report the public bind source using its exact `/host_mnt`
VM prefix and may retain `host-gateway` in inspected ExtraHosts. The fixture
checks the exact canonical public-file source in either observed fixture
representation, bind type, configured/effective readonly flags, actual file bytes
and actual container resolution; it never accepts parent mounts or infers trust
from a successful TCP connection. Cleanup is registered before faults and
reconciles only owned resources, preserving uncertainty. Skips for absent opt-in
settings are incomplete provider evidence. The complete new suite joins the
existing frozen provider gates after independent reviews; it does not establish
browser/system trust adoption or exact published-HEAD CI.

## Runnable macOS quickstart integration

The shipped [macOS example](../examples/macos-quickstart/README.md) has a separate
complete native suite. Install its explicit application dependencies (Node 26/npm)
and run serially under an owned native/gateway fixture grant:

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_NODE_TEST_BINARY=/absolute/path/to/node \
BACKLOT_NPM_TEST_BINARY=/absolute/path/to/npm \
  go test -race ./internal/acceptance -run '^TestMacOSQuickstart$' -count=1 -v
```

It copies the actual application/lockfile into an owned temporary Git repository,
creates two real worktrees, and runs `npm ci --ignore-scripts` in each (registry
access or a populated npm cache is needed). It verifies frontend/native SSR/API,
variable text/binary WebSockets, canonical redirects/secure cookies, success and
failure reports retained after teardown, distinct ports/origins, stop/start and
CA persistence, missing Node trust, occupied ports, unavailable gateway recovery
and preservation of peers/unrelated listeners. It retains private daemon evidence
at its reported short root and removes owned worktrees only after verified cleanup.
Only explicit private fixture clients/daemon trust the generated root; it never
changes system/browser trust or DNS. Missing opt-in tools skip; skips are not
acceptance. Node/npm remain optional application dependencies, outside `make check`.

For the managed quickstart slice, run this complete suite, the complete managed
HTTPS suite above (including the explicit Node setting), gateway fixture contracts,
`make check`, and the separate real macOS/browser adoption walkthrough on the frozen
candidate. These demonstrate the bounded host/browser/native path; full external
container/release gates below remain required before a broader release declaration.
Published-HEAD CI is still required. Record real browser/system trust actions and
baseline DNS/trust configuration separately; private fixture roots cannot prove them.

## Public release workflows

The macOS ARM64 opt-in public workflow suite uses the runnable generic fixture,
real temporary Git worktrees, normal host/container DNS and operator-approved TLS.
It exercises CLI operations through a private owned daemon and retains sanitized
command timings in its test log; raw state/logs/artifacts remain private at the
reported root. Follow [fixture setup](../examples/release-acceptance/README.md),
then run:

```sh
BACKLOT_ACCEPTANCE_CONFIG=/absolute/private/machine.yaml \
BACKLOT_ACCEPTANCE_PROJECT_INPUTS=/absolute/private/inputs.yaml \
  go test -race ./internal/acceptance -run '^TestReleaseWorkflows$' -count=1 -v
```

The separate private inputs document must select explicitly built fixture/browser
image tags; the machine file supplies providers only. Each temporary fixture
repository is explicitly enrolled under its owned private root. This
suite mutates only its owned instance routes/resources, never gateway bootstrap,
DNS or trust. An unset variable skips; a skip is not acceptance. This supplements
the complete Docker/gateway/HTTPS suites above, which remain separate required
frozen-candidate gates. For durable raw fixture state, set the test-only
`BACKLOT_ACCEPTANCE_TEMP_ROOT` to an existing short private absolute directory.
The default remains `/tmp` to fit macOS Unix socket paths; an overlong explicit
root fails instead of silently falling back. `TMPDIR` separately controls Go
build/test scratch, not the acceptance fixture root. Record exact commit/dirty patch, image/config provenance,
failed/skipped runs, timing and cleanup outside the source tree as described in
the [release verification guide](release-acceptance/README.md).

## Linux runtime and disposable browser gate

First build/install the static release binary and run its IPC/native lifecycle gate:

```sh
make release-build RELEASE_GOOS=linux RELEASE_GOARCH=amd64
install -m 0755 bin/backlot-linux-amd64 /your/user/bin/backlot
BACKLOT_INSTALLED_TEST_BINARY=/your/user/bin/backlot \
  go test -race ./internal/daemon -run '^TestLinuxInstallation$' -count=1 -v
```

This verifies the installed executable's private IPC/state permissions, resource-free
persistent restart and cooperative descendant cleanup. Then run the early HTTPS
vertical slice before lengthy provider regression suites:

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///var/run/docker.sock \
BACKLOT_INSTALLED_TEST_BINARY=/your/user/bin/backlot \
BACKLOT_BROWSER_TEST_BINARY=/absolute/path/to/chromium \
BACKLOT_CERTUTIL_TEST_BINARY=/usr/bin/certutil \
  go test -race ./internal/daemon -run '^TestLinuxHTTPS$' -count=1 -v -timeout=15m
```

With `BACKLOT_INSTALLED_TEST_BINARY`, this uses the installed static CLI/daemon
(and builds the native Go application); otherwise it builds a race CLI fixture,
which does not establish installed CGO=0 networking evidence. It verifies canonical host-only and
bridge HTTPS with explicit read-only public-root inputs, correct/missing/wrong
trust, SSR/API/WebSockets/redirects/cookies and lifecycle cleanup. Optional browser
settings enable an additional bounded Linux browser gate: a fresh private HOME,
profile and NSS database reject missing trust, then accept only the public root
with normal canonical DNS. Both browser variables are required together; CI sets
both. No TLS ignore flags, resolver overrides or system/user trust mutation are
used. A run without browser settings is incomplete browser evidence.

The short gate does not replace `make check` or the complete gateway, Docker,
HTTPS, managed HTTPS, container-managed HTTPS and disposable evidence suites.
Run those serially on the frozen candidate and preserve exact revision/tool/image
provenance, failures/skips, timings and authenticated cleanup. macOS regression
requirements remain unchanged. An unavailable configured prerequisite fails;
absent opt-in settings skip and are never passes. Exact published-HEAD CI remains
required for readiness. Debian actual-host and Ubuntu fresh-runner results are
separate evidence, and neither establishes WSL UX.


## Scoped doctor integration

With an exclusive owned fixture grant, stock Caddy, local Docker endpoint and an
already-present diagnostic image satisfying the [probe contract](daemon.md#explicit-owned-container-probe):

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
BACKLOT_DOCTOR_TEST_IMAGE=golang:1.27.1 \
  go test -race ./internal/daemon -run '^TestDoctor(Runtime|OwnedProbeCancellationAndRecovery)$' -count=1 -v
```

The runtime fixture also requires preexisting `alpine:3.21` for its selected
application consumer. It uses normal DNS, explicit private fixture daemon trust,
verified container TLS and a dynamic explicitly IPv4-wildcard HTTPS fixture bind;
admin stays loopback. It never changes existing gateways, machine/browser trust
or DNS. The fixture bound listener is joined on cleanup. These tests include
nonzero missing-trust failure/recovery, provenance drift, deferred unavailable
runtime, retained data, cancellation after creation/start, shutdown joins,
lost-create-effect recovery and mismatched-authority preservation. The public-CA
probe verifies a certificate-only 0444 file bind under a private 0700 parent while
retaining capless root, read-only rootfs, no-new-privileges and the five-second TLS
deadline. Source CA/key bytes and modes remain private and unchanged. Focused
`TestDoctorPublicCA*` tests cover bounded pinned-certificate validation, partial
allocation, tampering, consumer uncertainty and inspection/absent-file recovery.
Missing opt-in
settings skip and do not establish provider evidence. The ownership suite also
requires Docker CLI's classic builder (`DOCKER_BUILDKIT=0`) to build a uniquely
labeled healthcheck fixture from the exact inspected local diagnostic base ID,
with `--pull=false --network=none`. A positive control proves the inherited
healthcheck executes; the actual probe's effective Engine config disables it
and has no healthcheck execution state. Consumers stop before the exact created
image is removed and its absence verified. Production doctor never pulls/builds
images; production/default gateway exposure stays unchanged.

## Native watcher example and browser HMR

Prepare a private copy of `examples/native-watchers` with Go 1.27.1, Air 1.67.4,
Node 26.8.1 and `npm ci --ignore-scripts` (Vite 8.3.4). Put those tools on the
fixture daemon PATH. The test mutates the private copy's `main.js`; never point it
at a working checkout. Select an existing private empty fixture root on a short
path, explicit stock Caddy, and Linux Chromium/certutil:

```sh
BACKLOT_WATCHER_TEST_PROJECT=/absolute/private/native-watchers \
BACKLOT_WATCHER_TEST_ROOT=/tmp/private-watcher-fixture \
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
BACKLOT_BROWSER_TEST_BINARY=/absolute/path/to/chromium \
BACKLOT_CERTUTIL_TEST_BINARY=/usr/bin/certutil \
  go test ./internal/daemon -run '^TestNativeWatchers$' -count=1 -v -timeout=9m
```

This builds the real race CLI, launches ordinary Air/npm through the daemon,
uses default canonical hostname resolution and verified TLS, rejects missing
browser trust, imports only the fixture public root into disposable HOME/NSS,
and checks an HMR edit in the same browser document. Authenticated teardown and
browser joins precede fixture removal. A failed cleanup preserves private state;
no TLS bypass, resolver override, user/system trust mutation or Docker is used.
This bounded browser gate does not establish macOS system/browser adoption or
replace frozen native/provider/regression and exact published-content CI gates.
