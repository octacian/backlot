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
| `make release-snapshot` | Both unpublished GoReleaser archives and checksums under `dist/` |
| `make release-package` | Clean exact stable-tag archives, without publication |
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
| `make skills-generate`, `make skills-check` | Generate/verify independent installed skill references, schemas and examples |
| `make schema-check` | Reject stale generated schemas without writing |
| `make check-implementation` | Run schema/notice drift checks, formatting, vet, lint, Go race tests, and build |
| `make check-docs` | Run ADR tooling tests and verify ADR records |
| `make check` | Run both implementation and documentation checks |

Before committing Go/tooling changes, run `make check`. Documentation changes
require `make check-docs`, `git diff --check`, and manual checking of links,
commands, and consistency with current behavior and applicable decisions. Run
additional focused checks when changed behavior warrants them; do not add tests that merely
duplicate implementation details.

## CI and platform evidence

GitHub Actions runs shared checks on macOS ARM64 and Ubuntu 24.04 amd64. A separate
Ubuntu runtime job installs a reproducible Linux binary and runs real native,
Docker, Caddy and disposable-browser gates.
Debian actual-host results are distinct from Ubuntu CI; routine releases do not
require a new Debian run. WSL is configuration guidance only. See [platform policy](platforms.md).
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
| Go/runtime sources, schemas, non-CI examples, `install.sh`, module/build/lint configuration | macOS/Ubuntu repository checks, Linux runtime, current-head scenes and project inputs |
| `THIRD_PARTY_NOTICES.md` | Repository and Linux installation checks, plus Docs |
| `actions/` implementation | Current-head scenes and project inputs |
| `examples/ci-scenes/` implementation | Scenes |
| `examples/ci-inputs/` implementation | Project inputs |
| Native recipe/runtime implementation or `.github/workflows/recipes.yml` | One serial native recipe job; one Chromium and DB-only compatibility checks |
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
not demonstrate Docker integration or HTTPS publication; select the relevant
provider suites below for those boundaries. Runtime integration tests must use
owned resources and must never modify an operator's unrelated containers, routes,
files, or databases.

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

## Release packaging and publication

Follow [Preparing a release](preparing-a-release.md) for release policy, CI
readiness, note curation, manual publication and failed-draft recovery.

Pinned GoReleaser OSS v2.19.0 owns target builds, deterministic archives and SHA-256
files. Make installs it under `.tools/bin`; its source build may download its
required Go toolchain (currently Go 1.27.3), while Backlot builds use `go.mod`'s
toolchain. Configuration validation and notice drift checks precede packaging.
Snapshot names start `dev-` and can include working changes; retain the dirty patch
with evidence.

The [Release archives workflow](../.github/workflows/release.yml) validates relevant
PR/main changes without publication permission. It extracts the exact archives on
macOS ARM64 and Ubuntu amd64, checks file lists, checksums, version and standalone
`licenses` output, then invokes `TestInstalledBinary` for private daemon startup,
restart, startup failure and cleanup. Set `BACKLOT_INSTALLED_TEST_BINARY` to an
absolute extracted binary to run that fixture locally or on actual Debian; it
does not establish broader HTTPS/runtime coverage. Only the final draft job
receives `contents: write`; fork PRs receive no release credentials.

For reproducibility, prepare independent source trees and build caches at the
same revision/dirty patch with the same Go/GoReleaser versions, run
`make release-snapshot` in each, compare both archives and checksum files with
`cmp`, and inspect `go version -m` and extracted file modes. Real-tag packaging
uses source commit time and normalized archive ownership/modes/time, not wall clock.
Metadata unit checks cover Go-install module fallback and linked/local identity;
release guard tests cover trusted exact-SHA main evidence, newest runs/latest
attempts, pagination, invalid tags, existing draft refusal, upload verification
and draft-only behavior using local boundary stubs. Workflow predicate fixtures
exercise skipped-ancestor eligibility and required failure/cancellation paths;
they do not prove live GitHub scheduling or publication.

The opt-in suites below provide [targeted diagnostics](release-acceptance/README.md)
for risks lacking CI coverage. Report actual coverage and limits without treating
skips as passes.

## Daemon state migrations

Follow the [retained migration decision](adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).
Before a real format change, capture a nonempty closed database with the unmodified
predecessor producer; retain its pinned revision, build settings/hashes, fixture
inputs and regeneration commands. The committed [v9 fixture](../internal/daemon/testdata/state/v9/README.md)
is the forward baseline. Never recreate historical evidence using current structs.
Pre-v9 formats are unsupported; retain fixture coverage from v9 onward.

Add the smallest private Go step in `internal/daemon/state_upgrade.go` with explicit
source/destination versions. Validate source layout/authority before converting;
retain the historical readers required by that source, without imposing current
buckets/fields first. Advance `api/v1.StateVersion` only for a real persisted change.
Retain earlier steps and fixtures; removal requires a compatibility decision.
Steps use the supplied transaction, never commit/open another transaction or touch
providers, processes, registry or retained files. Do not normalize unrelated values.
Current layout/authority checks and the final fence run after the complete path.

First exercise a copy of the oldest retained fixture through the full path and
reopen it, checking typed reads and unchanged bytes outside intended transforms.
Reject altered unsupported/corrupt copies without changing database bytes or
passive files; never run recovery on historical paths. Adapt expected transformed
records with each real change. Reuse the transaction rollback, startup cancellation,
controlled before/after-commit interruption and postcommit failure tests. The
synthetic 100→101→102 tests exercise actual bucket/value transformations, later-step
failure and cancellation after tentative writes through the production dispatcher.
These are algorithm evidence, not additional supported production versions.

```sh
go test ./internal/daemon -run '^Test(State|RetainedStateNine|CanceledStartup)' -count=1 -v
make check
```

The native-only fixture does not prove historical Docker/gateway compatibility.
Run additional provider suites only when changes reach those boundaries; do not
multiply lifecycle suites for every retained version.

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
a configured but unavailable provider is a failure. `make check` alone does not
prove Docker integration. When selecting this diagnostic, retain the tested
revision, relevant inputs, failures/skips and cleanup as described in the
[diagnostic guide](release-acceptance/README.md).

Focused disposable provider coverage (run serially with the same resource grant):

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestDisposableEvidenceDocker$' -count=1 -v
```

This exercises container artifacts before removal, historical logs after destroy,
exact job results, kept failure/cancellation/collection errors and healthy services,
and injected directory-ownership cleanup failure with explicit recovery. Select
it when changes affect these guarantees beyond existing CI coverage.
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
setup. When changes introduce uncovered operator DNS/trust risks, select real
host/browser/container checks for the affected paths. Record the tested revision,
relevant inputs, failures/skips and cleanup; follow the
[diagnostic guide](release-acceptance/README.md). Mock success cannot establish
operator acceptance.

## Managed local-CA gateway integration

Run the native managed suite with the same exclusive owned fixture grant:

```sh
BACKLOT_CADDY_TEST_BINARY=/absolute/path/to/caddy \
  go test -race ./internal/daemon -run '^(TestManagedHTTPSRuntime|TestManagedForceRestart|TestGatewayDefaultConfig)$' -count=1 -v
```

Select this suite for affected managed-HTTPS guarantees lacking CI coverage. It builds
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

These tests do not prove standard macOS/browser adoption setup. If a change
needs manual trust validation, use an isolated disposable macOS environment,
stock separate `caddy trust`, normal hostname resolution and a browser accepting
the certificate without a warning. Report unavailable validation honestly; a TLS
bypass, resolver override or browser proceed-anyway is never trust evidence.

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
settings are incomplete provider evidence. This suite does not establish
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

Select this suite when quickstart changes need coverage beyond CI. Record any
separately selected browser/system trust checks and baseline DNS/trust setup;
private fixture roots cannot prove them. A full adoption walkthrough is not a
routine PR or release requirement.

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
DNS or trust. An unset variable skips; a skip is not acceptance. This optional
suite diagnoses the four application workflows when relevant to uncovered change
risks. For durable raw fixture state, set the test-only
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
  go test -race ./internal/daemon -run '^TestInstalledBinary$' -count=1 -v
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

This bounded suite does not prove every provider guarantee. Select additional
suites only for affected risks lacking CI coverage, run mutating fixtures serially,
and retain the tested revision, relevant inputs, failures/skips and cleanup.
An unavailable configured prerequisite fails; absent opt-in settings skip and are
never passes. Applicable required CI at the exact published revision establishes
routine readiness. Debian actual-host and Ubuntu fresh-runner results remain
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
This bounded browser check does not establish macOS system/browser adoption or
replace applicable required CI at the exact published revision.

<a id="early-shared-mariadb-checkpoint-43-gate-1"></a>
<a id="shared-mariadb-lifecycle-and-recovery-43-gate-2"></a>

## Shared MariaDB integration

The smaller [native Go/MariaDB](../examples/native-go-mariadb/README.md) and
[loopback Playwright](../examples/native-playwright/README.md) adopter recipes
have a separate, opt-in repository verification checkpoint:

```sh
BACKLOT_RECIPE_PLAYWRIGHT_PROJECT=/absolute/installed/native-playwright \
PLAYWRIGHT_BROWSERS_PATH=/absolute/owned/browser-cache \
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test ./internal/daemon -run '^(TestNativePlaywrightRecipe|TestNativeGoMariaDBRecipe|TestMariaDBRecipeCompatibility)$' -count=1 -v -timeout=10m
```

Install the recipe's pinned dependencies/browser separately with an explicit
owned `PLAYWRIGHT_BROWSERS_PATH` and `PLAYWRIGHT_SKIP_BROWSER_GC=1`, then pre-pull its
explicit MariaDB patch image. The checkpoint covers success, assertion failure,
recovery, original exits, exact arguments, portable exports and authenticated
teardown. The compatibility check first proves application authentication and
query/read/write access, then rejects a deliberately wrong environment password
against that same initialized database. A failed fresh startup does not establish
authentication rejection. It verifies exact owned Docker objects are absent after
destroy.

`BACKLOT_RECIPE_MARIADB_IMAGES` selects a comma-separated image list, defaulting
to the single adopter image. The [serial workflow](../.github/workflows/recipes.yml)
selects `mariadb:10.11.19,mariadb:11.4.13,mariadb:11.8.9,mariadb:12.3.3`, builds
the CLI/readiness/test executables once and installs one pinned Chromium.
`BACKLOT_INSTALLED_TEST_BINARY` and `BACKLOT_RECIPE_READY_BINARY` reuse those
explicit executables; absent overrides build private fixture binaries.
`BACKLOT_RECIPE_EXPORT_ROOT` can select an existing private evidence parent;
only audited exports beneath it are uploaded. Raw daemon state stays private.

Browser exports are checked for the selected test, resolved URL, status and valid
HTML, screenshots and ZIP traces. Go events must identify the selected test without
skipping; its report must match the original exit. The first audited export is
rechecked after recovery.
Representative cancellation covers a held browser SIGINT and Go SIGTERM on 11.4.
Present partial evidence is checked while missing final reports are allowed; see
[native runner cancellation](evidence.md#native-runner-cancellation). Cleanup
observations can conservatively fail on PID reuse and cannot rule out unseen
descendants. No skip establishes acceptance; these bounded checks do not replace
shared-server/provider gates or establish other-platform coverage.

Run serially with exclusive owned-fixture authority, an explicit local Docker Unix
endpoint and existing MariaDB 11.4/Alpine 3.21 images. Fixtures build the static
application image from the inspected Alpine ID with `--pull=false --network=none`;
production never pulls/builds. An unset endpoint skips (incomplete evidence); an
unavailable configured endpoint fails.

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^(TestSharedMariaDB.*|TestSharedUnselectedOfflinePrivacy)$' -count=1 -v -timeout=30m
```

Focused CLI/permission/configuration coverage:

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestSharedMariaDBGate1$' -count=1 -v -timeout=6m
```

See [CLI/permissions](../internal/daemon/shared_integration_test.go),
[lifecycle/concurrency](../internal/daemon/shared_lifecycle_test.go),
[ownership/privacy](../internal/daemon/shared_ownership_test.go),
[effect/acknowledgment faults](../internal/daemon/shared_faults_test.go) and
[force recovery](../internal/daemon/shared_force_integration_test.go) for scenarios;
[checkpoint helpers](../internal/daemon/shared_checkpoint_test.go) own private barriers. Parent-written random SQL witnesses must
survive independently of application initialization. Register cleanup before effects
or faults; fault/reap retained children only after observing real effects. Recovery
retains the five-second API expectation; the aggregate timeout allows serial fixtures.
Prove SQL database/user and owned runtime/image absence; retain cached base images
and uncertain diagnostics.

Within each test process, shared SQL fixtures lazily build one race-enabled host
CLI, one non-race host consumer and one static Linux consumer (`GOOS=linux`,
`CGO_ENABLED=0`). TestMain owns these immutable binaries through all scenario
cleanup and removes them after a successful suite; failures retain their directory
for diagnosis. Each new test process builds from current sources using the existing
Go build cache. Missing provider opt-in and private supervisor/anchor helper paths
do not initialize the bundle. Images, private state and SQL allocations remain
independently owned by each scenario. Both engines' MainContracts checks cover
ordinary Inspect/Fixtures/Logs/Shared redaction alongside selected startup disclosure.

Select related [DockerRuntime](#dockerprivate-state-integration) and disposable
evidence checks where changed guarantees lack CI coverage. Run mutating fixtures
serially and record the tested revision, relevant inputs, failures/skips and cleanup
per [targeted diagnostics](release-acceptance/README.md).
Prior MariaDB evidence does not establish PostgreSQL/custom execution, other-platform,
full-feature or published-revision CI readiness.

## Shared PostgreSQL integration

Use the same owned fixture rules as [shared MariaDB](#shared-mariadb-integration),
with an explicit Docker Unix endpoint and cached `postgres:17` and `alpine:3.21`.
Coordinate overlapping fixture windows; this requires no global engine/daemon
ownership. Before broad provider regression, exercise the actual CLI/daemon native
and bridge path, named-schema/trusted-extension permissions, wrong-auth/peer/admin
denial, parent-written persistence and preserved credential-drift cleanup/retry:

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestSharedPostgreSQLEarlyFlow$' -count=1 -v -timeout=8m
```

The complete suite covers lifecycle, permissions, interrupted SQL effects,
bootstrap/credential drift, force/dependency fences, stalled connections and
cancellation. The Linux runtime job pre-pulls PostgreSQL 17 and runs:

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestSharedPostgreSQL.*$' -count=1 -v -timeout=30m
```

The early-flow check does not replace this suite, the runnable example or applicable
repository/provider/CI gates. Follow the linked shared fixture rules for owned
cleanup and revision-bound evidence; missing opt-in configuration supplies no
provider evidence.

## Execution-scoped image handoff integration

With the explicit private Docker fixture grant and cached `alpine:3.21`, run:

```sh
BACKLOT_DOCKER_TEST_ENDPOINT=unix:///absolute/local/docker.sock \
  go test -race ./internal/daemon -run '^TestBuildImagesDocker$' -count=1 -v -timeout=8m
```

The suite builds its own labelled images using the classic builder and exact local
base ID, without pulls. It runs selected native project producers and real daemon
consumers, retained pin reuse, same/changed-ID restart, explicit compatibility
warnings and parent-written data witness, failed/missing/cancelled handoffs,
recovery and independent worktrees/disposable runs. Cleanup joins owned daemons,
reconciles their private journals and removes only fixture-labelled images/tags;
cached bases remain. Missing opt-in skips and does not establish provider evidence.
Configured provider/tool/image failures are failures. The complete existing Docker
runtime/disposable suites remain separate required frozen-candidate regressions.

## Agent skills

`make skills-generate` copies canonical guide sections, schemas and examples into
each skill's `references` and `assets` directories. Edit sources, not generated files.
`make skills-check` runs in `make check-implementation`; changes
to canonical docs or skills select that CI gate as well as applicable docs checks.
Generation is Go-only; the optional installer adds no contributor/runtime dependency.
Focused tests cover section selection, drift, local links/anchors and example schemas.

When command contracts/procedures change, update the two skill entry points and
canonical guides, regenerate, and review maintained versus generated diff size.
Supplemental upstream links in `cmd/skills` use a reviewed source baseline; review
that baseline with link-target changes, without treating it as a binary allowlist.
Evaluate unfamiliar setup, existing tests, manifest drift, missing prerequisites,
peer worktrees and kept failures with independent agents on isolated owned fixtures.
Record source/model/environment, task outcome, unnecessary commands, reference
consumption and cleanup mistakes outside the tree; fix demonstrated failures.

First prove actual application success and failure outside a Backlot checkout.
Supply an explicit private machine file with `--config` for
every configuration-composing plan/start/restart/doctor/config call; private daemon
and registry paths alone do not isolate operator machine defaults. Smoke selective
local and remote installs, each skill alone and both together, project/global
scopes, and source-independent references. Redirect
HOME/XDG/agent configuration only for the installer process into private temporary
directories; never overwrite real global skills. Follow
[installation/update guidance](agent-skills.md#installation-and-updates).
Provider/platform checks establish only the tested environment. Keep raw private
state and credentials out of reports.
