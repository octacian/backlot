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

`@latest` follows Go's version selection: before a release exists it resolves to
a pseudo-version, and after stable tags exist it normally selects the highest
release. It is convenient but not a reproducible pin; `@main` also moves. Once
`v0.1.0` is published, pin an installation or CI job with:

```sh
go install github.com/octacian/backlot/cmd/backlot@v0.1.0
backlot version --json
```

Version JSON reports linked release identity or Go's resolved module version,
including pseudo-versions. A full commit is reported when available; tagged Go
module installations may carry only their exact module version. `go version -m
"$(command -v backlot)"` additionally shows build settings. Local checkout builds
report `dev`/`dev-dirty`, and unpublished archives use `dev-REVISION`; these are
not official releases.

## Release archive installation

After publication, select an exact release from
[GitHub Releases](https://github.com/octacian/backlot/releases). Download
`backlot_v0.1.0_darwin_arm64.tar.gz` for macOS ARM64, or
`backlot_v0.1.0_linux_amd64.tar.gz` for Ubuntu 24.04/Debian 13.4 amd64, together
with `backlot_v0.1.0_checksums.txt`. Other targets are not supplied. In an empty
directory containing those downloads, verify the selected archive's line before
extraction (the manifest also lists the other target):

```sh
checksum=$(grep '  backlot_v0.1.0_darwin_arm64.tar.gz$' backlot_v0.1.0_checksums.txt) &&
printf '%s\n' "$checksum" | shasum -a 256 -c - &&
tar -xzf backlot_v0.1.0_darwin_arm64.tar.gz &&
./backlot version --json &&
mkdir -p "$HOME/.local/bin" &&
install -m 0755 backlot "$HOME/.local/bin/backlot"
```

On Linux substitute the Linux archive name and use `sha256sum -c -` for the
checksum command. Require an `OK` result and matching `v0.1.0` identity; missing
or mismatched checksum entries are failures. Put `$HOME/.local/bin` on PATH and
use `command -v backlot` to check which installation wins. Archives contain just
the executable, LICENSE and notices. Checksums detect damaged bytes; they do not
authenticate a publisher. Archives are unsigned/unnotarized and install no Caddy,
Docker, dependencies or trust. Keep macOS security policy decisions explicit.

## Upgrade, removal and compatibility

Prefer CLI and daemon from the same release. Before replacing the binary, use
the old matching binary to stop the daemon in each selected state directory:
`/path/to/old/backlot daemon stop --state-dir /absolute/private/state --json`.
Wait for exit/lock release, install the verified new binary, then explicitly run
`backlot daemon start --state-dir /absolute/private/state --json`. Binary upgrade
preserves private state and CA identity; do not delete them to bypass a version
error. Existing API/state checks reject incompatible pairings. See
[state compatibility](containers.md#state-compatibility) for retained forward
upgrades from v9 and matching-binary recovery; pinning an older executable does
not make newer state readable. Downgrades/reversible migrations are not promised.

`v0.1.x` patches preserve documented CLI invocation/exit behavior, JSON/local API,
manifest/machine configuration and retained-state upgrade handling. Breaking
changes require a new `0.x` minor and migration notes. The executable release,
wire/schema namespace and persisted-state format are independent versions;
`1.0.0` is reserved for a deliberately stable public API. Use the exact tag's
[documentation](https://github.com/octacian/backlot/tree/v0.1.0/docs) and
[schemas](https://github.com/octacian/backlot/tree/v0.1.0/schemas) after publication,
and read its release/upgrade notes. Published tags and assets never move or get
replaced; corrections get a new patch version.

To remove, first stop the daemon with its matching binary, then delete only the
installed executable at the path you selected (for example
`rm "$HOME/.local/bin/backlot"`).
For Go installs use the actual `GOBIN`/`GOPATH/bin` path. State, logs, artifacts,
databases and CA identity remain for explicit lifecycle/retention handling; removal
does not revoke system/browser trust or clean provider resources implicitly.

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
paths and excludes automatic VCS stamping, wall-clock time and Go build IDs. It
links the source commit/time while retaining a `dev` version. Rebuild twice with
the same Go toolchain/modules/flags and source identity, compare with
`cmp`, and record SHA-256 and `go version -m`. Cross-builds do not prove runtime
support. No release distribution, signing, notarization or installer is promised
by this plain binary command. `make release-snapshot` installs pinned GoReleaser
OSS locally and prepares both unpublished archives/checksums under ignored
`dist/`; `make release-package` requires a clean tagged checkout and produces the
real-tag form without publishing. Their declarative configuration is
[.goreleaser.yaml](../.goreleaser.yaml). See [release automation](testing.md#release-packaging-and-publication)
for checks and publication responsibilities.

Every binary, including ordinary source builds and `go install`, embeds the full
[LICENSE](../LICENSE) and generated [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md).
Run `backlot licenses` to print both; copying the executable alone preserves
attribution without adjacent files or a share directory. Archives also retain
readable copies. Notices include linked CLI/optional acceptance application
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
all four application workflows and explicit image setup commands as optional
diagnostics. Follow [release preparation](preparing-a-release.md) for readiness checks.

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
| Missing tool/input/seed | Compare offline `plan` paths with `doctor --project PATH --scene NAME` in the daemon context; errors show the attempted tool and declaration source. For a local tool override use `project setup --edit`; configure required inputs/seeds explicitly. |
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

To refresh the daemon's startup PATH after changing shell/tool setup, stop it
normally, wait for exit/lock release, then explicitly start it from the intended
context with the same `--state-dir`. Keep the state directory and retained data.
Recheck scoped doctor; apply changed selection to a scene only through its existing
explicit start/restart lifecycle. See [daemon commands](daemon.md#commands).

Never remove retained data while a consumer may still run. A cleanup failure is a
failure, with remaining handles preserved for recovery. Private state and captured
application logs may contain secrets; publish only reviewed sanitized evidence.
