# Install and operate Backlot

For macOS/Linux targets, prerequisites, WSL guidance and planned Windows work,
see [platforms](platforms.md). Install Go 1.27.1 or a compatible newer patch, Git
for worktrees, and `lsof` (included with macOS); Make is needed only for source
builds. Native commands must stay in their supervised process group.

Install Docker only for container scenes. Published scenes require
[managed local HTTPS or external Caddy](https.md). Trust, external DNS/public
certificates, toolchains and system services remain explicit operator setup;
Backlot never installs system/browser trust.

## Go installation

```sh
go install github.com/octacian/backlot/cmd/backlot@latest
backlot version --json
```

The executable package is `cmd/backlot`; the module root is not an executable.
Go installs into `GOBIN` when set, otherwise into `bin` under the first `GOPATH`
directory (default `$HOME/go/bin`). Put that directory on PATH if it is not already
there. This path downloads and builds the command without a repository checkout,
Make or a separate `go mod download` step. It does not install the example files;
the [macOS quickstart](../examples/macos-quickstart/README.md#prepare-once) explains
how to obtain those.

There are currently no release tags, so `@latest` resolves to a pseudo-version
from `main`. Future release tags follow Go's normal version selection. The command
currently reports `dev` version metadata; use `go version -m` on the installed
binary to inspect its resolved module version and build settings.

## Reproducible source build and dependency notices

For a reviewed checkout and reproducible local build, install Make, then:

```sh
git clone https://github.com/octacian/backlot.git
cd backlot
```

Review/select the source revision before building:

```sh
go mod download
make release-build
bin/backlot-darwin-arm64 version --json
shasum -a 256 bin/backlot-darwin-arm64
install -m 0755 bin/backlot-darwin-arm64 /your/user/bin/backlot
```

For Linux amd64, use `make release-build RELEASE_GOOS=linux RELEASE_GOARCH=amd64`
and install `bin/backlot-linux-amd64` instead. Run `sha256sum` on Linux.

`release-build` defaults to `GOOS=darwin`, `GOARCH=arm64`, supports explicit
`RELEASE_GOOS`/`RELEASE_GOARCH`, fixes `CGO_ENABLED=0`, trims build
paths and excludes VCS dirtiness, timestamps and Go build IDs. It deliberately
uses stable `dev` version metadata until release versioning is separately
specified. Rebuild twice with the same Go toolchain/modules/flags, compare with
`cmp`, and record SHA-256 and `go version -m`. Cross-builds do not prove runtime
support. No release distribution, signing, notarization or installer is promised
by this local build command.

Ship [LICENSE](../LICENSE) and [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md)
with any copied binary. Notices include linked CLI/optional acceptance application
modules and the Go standard library. `make notices` regenerates from downloaded
pinned source; `make notices-check` rejects drift. Inventories label macOS ARM64
and Linux amd64 CLI dependencies separately from the optional macOS/container application. Reviewed
source-embedded notices live in `cmd/notices/supplements.json`, bound to exact
module versions and source SHA-256; dependency/source changes require attribution
review before regeneration. The current linked-source audit includes the wsutil
UTF-8 decoder notice, fiat-crypto scalar notice, and ported YAML source notices.
The inventory keeps fixture-only dependencies distinct from CLI dependencies.
The acceptance application uses
MPL-2.0 MySQL driver source unmodified, obtainable at its recorded module version
with `go mod download -json github.com/go-sql-driver/mysql@v1.9.3`. Its license
notice identifies covered source obligations; the fixture driver is not linked
into `backlot`. Docker/Chromium distributions carry their own license notices;
their images are operator-built setup dependencies, not bundled binary contents.

For the shortest existing-project path, start with the [README quickstart](../README.md#run-an-existing-configured-project).
For included application code, a minimal manifest and explicit trust settings, use
the [macOS HTTPS quickstart](../examples/macos-quickstart/README.md). Contributor
validation (`make check`) is required when changing Backlot, not to run a project.

## Explicit setup and daily use

Choose a short absolute private daemon state directory (0700). Put machine
configuration outside the repository (0600); do not commit credentials. Machine
provider settings and daemon state flags are separate. Follow
[manifest/configuration](manifest.md), [native daemon](daemon.md),
[containers/private state](containers.md) and [HTTPS setup](https.md) for exact
contracts. The [runnable fixture](../examples/release-acceptance/README.md) supplies
all four application workflows and explicit image setup commands.

```sh
backlot daemon start --state-dir /private/backlot --json
backlot doctor --state-dir /private/backlot --json
backlot plan dev --project /absolute/project --config /private/machine.yaml --json
backlot scene start dev --project /absolute/project --config /private/machine.yaml --state-dir /private/backlot --json
backlot list --state-dir /private/backlot --limit 100 --json
backlot status --state-dir /private/backlot --json INSTANCE_ID
backlot daemon stop --state-dir /private/backlot --json
```

`list` emits one bounded identity-ordered page, including historical/destroyed
instances. Continue with `--after NEXT_AFTER_ID`; concurrent additions with earlier
random IDs may require a fresh traversal. `status ID` inspects one recorded instance;
add `--follow` for bounded sampled observation. Use [named scenes](daemon.md#named-persistent-scenes) for checkout-based
start/status/stop; `daemon status` reports daemon connectivity and `gateway status`
reports managed process/listener readiness independently of TLS trust. Neither ordinary command
reveals private ownership authority or fixture secrets. bare `doctor` checks local
access/compatibility and shows state/socket/log paths plus an unloaded default
config candidate. Use `doctor --project PATH --scene NAME --config PATH` or
`doctor --instance ID` for scoped provider observations and defining/storage paths;
see [scoped diagnostics and opt-in container probes](daemon.md#scoped-provider-diagnostics). Raw API clients use the local permissioned Unix
socket, required API headers and named [contracts](daemon.md#local-api-and-doctor).

Closing a persistent run client leaves accepted startup daemon-owned. Disposable
runs have a finite renewing lease; client loss cancels after expiry. Ctrl-C and
SIGTERM request bounded cancellation. JSON run output is a single typed result;
application streams go through `logs` (JSON logs are NDJSON). Test exit status,
cancellation, infrastructure/collection/cleanup failures are independent fields.
A zero test exit cannot override failed collection/cleanup.

## Diagnosis and recovery

| Symptom | Diagnosis and next action |
| --- | --- |
| Cannot contact daemon | Use the same `--state-dir`; run `daemon status`/`doctor`, inspect the private startup log, then explicitly start. Never delete a live or uncertain socket/lock. |
| Missing tool/input/seed | Run offline `plan`; configure the declared tool/input or existing static seed. Daemon baseline environment comes from startup, not a later shell. |
| Image unavailable | Explicitly pull/build the declared image with Docker tooling, then restart the failed instance. Backlot never pulls implicitly. |
| Database TCP ready but application unavailable | Use application preparation or a command probe that verifies credentials/schema; do not equate an open port with application readiness. |
| Probe or preparation failed | Inspect component logs and failure detail; correct the application, then explicitly restart. Failed fresh-only initialization is not recorded as successful. |
| Scene failed after readiness | Read the required-service exit/supervision diagnostic and historical logs. No automatic restart; fix the cause before explicit restart. |
| Need machine/project/public CA paths | Use scoped doctor; distinguish effective scene inputs, recorded launch files and current file drift. `public_ca_certificate` locates the actual public root for explicit manual trust; no key is printed or trust installed. |
| Container origin fails while host succeeds | Use explicit `--instance ID --container-probe-image ALREADY_PRESENT_IMAGE`; compare default image trust against optional `--probe-public-ca`, DNS, TCP and normally verified TLS. Follow the documented image contract; no implicit pull/workload/resolver override occurs. |
| Public origin unavailable | Verify selected gateway status, normal DNS and TLS trust, and exact owned scope first. Check host and container consumers separately. Keep unrelated routes intact; never reload a stale bootstrap. |
| Manifest drift | Repeated run reports preserved snapshot drift. Restart applies compatible changes; retained storage/initialization/sensitive contract changes require explicit reset/destroy. |
| Kept disposable failure | Inspect status/logs/artifacts/fixtures, then explicitly destroy by ID. Successful runs still clean up. |
| Cleanup uncertain or foreign consumer present | Preserve journal/resources; restore provider access or remove only your verified consumer, then retry stop/destroy. Never prune by name, PID or hostname. |
| Daemon crash | Start the same binary/state directory; recovery stops verifiably owned survivors and marks interrupted, retains data/managed CA and reports evidence gaps. Inspect, explicitly start the managed gateway, then restart scenes. Unavailable gateway retains exact route cleanup uncertainty for retry. |
| Managed gateway uncertain after temporary control loss | Retry `gateway stop` in the same state directory. If still fenced, choose restored authority, controlled reboot or explicit survivor-risk acknowledgement with force; follow [managed recovery](https.md#managed-gateway-recovery). Retain configuration and CA. |
| State upgrade or unsupported version | Follow [state compatibility](containers.md#state-compatibility): stop the old daemon with its matching binary before upgrading, wait for exit/lock release, then explicitly start the newest binary in the same directory. Version 9 is the minimum supported baseline; retained steps from v9 support skipped releases without reset. Pre-v9 and other unsupported formats are rejected unchanged; preserve them for matching-binary diagnosis and lifecycle. |
| Evidence expired | Status retains `evidence_expired`; expired logs/artifacts cannot be recovered from deleted containers. Configure daemon age/size retention before runs. |

Never remove retained data while a consumer may still run. A cleanup failure is a
failure, with remaining handles preserved for recovery. Private state and captured
application logs may contain secrets; publish only reviewed sanitized evidence.
