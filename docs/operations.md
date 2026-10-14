# Install and operate Backlot on macOS ARM64

V1 runtime acceptance targets macOS ARM64. Install Go at the `go.mod` version
(currently 1.27.1), Git, Make and `lsof`. Native commands must cooperate with
Backlot's supervised process-group contract; do not detach or delegate to another
supervisor. Install Docker only for container scenes. For published scenes choose
[managed local HTTPS or external Caddy](https.md). Managed mode needs a configured
stock native Caddy executable and a separate explicit trust command; Backlot never
installs system/browser trust. External DNS/public certificates, toolchain
installation and system services remain operator tasks.

## Local binary and dependency notices

From a reviewed checkout:

```sh
go mod download
make release-build
bin/backlot-darwin-arm64 version --json
shasum -a 256 bin/backlot-darwin-arm64
install -m 0755 bin/backlot-darwin-arm64 /your/user/bin/backlot
```

`release-build` fixes `GOOS=darwin`, `GOARCH=arm64`, `CGO_ENABLED=0`, trims build
paths and excludes VCS dirtiness, timestamps and Go build IDs. It deliberately
uses stable `dev` version metadata until release versioning is separately
specified. Rebuild twice with the same Go toolchain/modules/flags, compare with
`cmp`, and record SHA-256 and `go version -m`. Cross-builds do not prove runtime
support. No release distribution, signing, notarization or installer is promised
by this local build command.

Ship [LICENSE](../LICENSE) and [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md)
with any copied binary. Notices include linked CLI/optional acceptance application
modules and the Go standard library. `make notices` regenerates from downloaded
pinned source; `make notices-check` rejects drift. Inventories label the macOS
ARM64 CLI separately from the optional macOS/container application. Reviewed
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
backlot run dev --project /absolute/project --config /private/machine.yaml --state-dir /private/backlot --json
backlot list --state-dir /private/backlot --limit 100 --json
backlot status --state-dir /private/backlot --json INSTANCE_ID
backlot daemon stop --state-dir /private/backlot --json
```

`list` emits one bounded identity-ordered page, including historical/destroyed
instances. Continue with `--after NEXT_AFTER_ID`; concurrent additions with earlier
random IDs may require a fresh traversal. `status` aliases recorded-instance
inspection; `daemon status` reports daemon connectivity and `gateway status`
reports managed process/listener readiness independently of TLS trust. Neither ordinary command
reveals private ownership authority or fixture secrets. `doctor` checks local
access/compatibility; provider checks are explicitly deferred. Validate selected
provider workflows separately. Raw API clients use the local permissioned Unix
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
| Public origin unavailable | Verify selected gateway status, normal DNS and TLS trust, and exact owned scope first. Check host and container consumers separately. Keep unrelated routes intact; never reload a stale bootstrap. |
| Manifest drift | Repeated run reports preserved snapshot drift. Restart applies compatible changes; retained storage/initialization/sensitive contract changes require explicit reset/destroy. |
| Kept disposable failure | Inspect status/logs/artifacts/fixtures, then explicitly destroy by ID. Successful runs still clean up. |
| Cleanup uncertain or foreign consumer present | Preserve journal/resources; restore provider access or remove only your verified consumer, then retry stop/destroy. Never prune by name, PID or hostname. |
| Daemon crash | Start the same binary/state directory; recovery stops verifiably owned survivors and marks interrupted, retains data/managed CA and reports evidence gaps. Inspect, explicitly start the managed gateway, then restart scenes. Unavailable gateway retains exact route cleanup uncertainty for retry. |
| Unsupported state version | Incompatible state is rejected without modification; use a matching state format. |
| Evidence expired | Status retains `evidence_expired`; expired logs/artifacts cannot be recovered from deleted containers. Configure daemon age/size retention before runs. |

Never remove retained data while a consumer may still run. A cleanup failure is a
failure, with remaining handles preserved for recovery. Private state and captured
application logs may contain secrets; publish only reviewed sanitized evidence.
