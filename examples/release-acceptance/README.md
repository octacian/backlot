# Runnable release fixture

This is a small application: Go HTTP API/UI and SQL preparation/tests, MariaDB,
and an optional Chromium test image. It contains no port allocator, ownership
labels, gateway writer or lifecycle cleanup wrapper. Backlot supplies those.
`message.txt` is read on each development request; production reads a build copy
and changes only at explicit restart. Application credentials are generated per
resource generation, with a separate database administrator secret.

From the repository root on macOS ARM64, with Go 1.27.1, Docker, Git, Make and
`lsof` installed:

```sh
make build
go build -o examples/release-acceptance/fixture ./examples/release-acceptance
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -o examples/release-acceptance/fixture-linux ./examples/release-acceptance
docker pull alpine:3.21
docker pull mariadb:11.4
docker build -t backlot-release-fixture:local examples/release-acceptance
docker build -t backlot-release-browser:local \
  -f examples/release-acceptance/Dockerfile.browser examples/release-acceptance
```

These are explicit setup builds/pulls. Backlot never implicitly pulls or builds.
The optional browser image installs Chromium `136.0.7103.113-r0` from Alpine 3.21
and contains Chromedp application test code; record the resolved image digests
with acceptance evidence. Browser sandbox disabling is scoped to the disposable
root-user test container, never the host browser. Browser tests wait for actual
DOM state after API and variable-payload WebSocket checks, then collect HTML,
a screenshot and a structured test report. The maintained MySQL driver handles
application SQL; Backlot remains database-agnostic.

Copy `machine.example.yaml` to a private file outside the checkout, set the local
Docker socket (`docker context inspect --format '{{.Endpoints.docker.Host}}'`),
and configure an **existing approved** wildcard DNS/TLS Caddy scope as described
in [HTTPS setup](../../docs/https.md). Do not overwrite the operator's gateway
configuration. Start an explicit daemon with a short private state path:

```sh
bin/backlot daemon start --state-dir /tmp/my-backlot --json
bin/backlot plan dev --project examples/release-acceptance --config /private/machine.yaml --json
bin/backlot run dev --project examples/release-acceptance --config /private/machine.yaml --state-dir /tmp/my-backlot --json
bin/backlot run production --project examples/release-acceptance --config /private/machine.yaml --state-dir /tmp/my-backlot --json
bin/backlot run backend-test --project examples/release-acceptance --config /private/machine.yaml --state-dir /tmp/my-backlot --json -- -run TestDatabase
bin/backlot run ui-test --project examples/release-acceptance --config /private/machine.yaml --state-dir /tmp/my-backlot --json -- -run TestBrowser
```

Each workflow is one `run` command after explicit setup. Open the returned origin
with normal browser trust. `/api/value` reads data; POST `{"value":"example"}`
mutates it. The Save value button uses that API. `/` performs SSR through the
canonical HTTPS origin. `/ws` echoes actual variable payloads; `/redirect` sets a
Secure/HttpOnly/SameSite cookie and redirects to the canonical origin. No TLS
bypass, host aliases, custom CA or fixture dialing override is used here.

The `-run` regular expression must select `TestDatabase` or `TestBrowser` for its
workflow; unmatched selectors fail clearly. `--fail` deliberately returns exit 1
after test work/report creation. `--wait 30s` holds a test for cancellation/client
loss and shared-output checks. Disposable terminal arguments go only to the
selected terminal job. `backend-test` report writes share a declared output group;
concurrent runs serialize the entire execution in one checkout. Independent Git
worktrees have independent output groups. Preview `dist`, UI `ui-dist` and test
`reports` are separate outputs, allowing previews and tests to coexist.

Record the instance IDs; flags precede IDs for inspection/destruction:

```sh
bin/backlot list --state-dir /tmp/my-backlot --json
bin/backlot status --state-dir /tmp/my-backlot --json INSTANCE_ID
bin/backlot fixtures --state-dir /tmp/my-backlot --component api --json DEV_INSTANCE_ID
# Sensitive output: inspect locally, never paste it into a log/report.
bin/backlot secret --state-dir /tmp/my-backlot --component api --json DEV_INSTANCE_ID password
bin/backlot logs --state-dir /tmp/my-backlot --json INSTANCE_ID
bin/backlot logs --state-dir /tmp/my-backlot --follow --component api INSTANCE_ID
bin/backlot stop --state-dir /tmp/my-backlot --json INSTANCE_ID
bin/backlot restart INSTANCE_ID --state-dir /tmp/my-backlot --json
bin/backlot reset INSTANCE_ID --state-dir /tmp/my-backlot --json
bin/backlot destroy --state-dir /tmp/my-backlot --json INSTANCE_ID
bin/backlot daemon stop --state-dir /tmp/my-backlot --json
```

Stop/start and restart retain data/passwords. Reset replaces them but retains the
logical ID/origin. Destroy removes owned runtime/data and leaves results/evidence
under the daemon retention policy. Stop all consumers before deleting fixture
build outputs. Remove fixture images with ordinary Docker tooling only after
verifying their exact IDs/ownership and absence of consumers; shared base images
are cache and may remain. Keep failed/uncertain cleanup state for diagnosis.

The public workflow acceptance suite creates its own temporary Git repository,
two real detached worktrees, daemon process and private state. It uses the same
manifest/application commands and configured images. It logs actual command
milliseconds, origins and results, preserves its private state/evidence path,
and destroys its recorded instances at cleanup. It does not use an operator's
normal daemon state or mutate whole gateway configuration:

```sh
BACKLOT_ACCEPTANCE_CONFIG=/private/machine.yaml \
  go test -race ./internal/acceptance -run '^TestReleaseWorkflows$' -count=1 -v
```

This opt-in suite is macOS ARM64 runtime evidence. A skip is not acceptance.
See the [release ledger](../../docs/release-acceptance/ledger.md) for actual runs,
remaining cases and the separate required provider/review/frozen-candidate gates.

The opt-in public acceptance suite adds owned negative scenarios to temporary
fixture manifests: failed fresh initialization/retry, consumed-port conflict,
missing local image, native/container service exits, live NDJSON following and
collection/uncertain cleanup failures. `witness-server` is the generic fixture
listener for these scenarios; its explicit `/exit` fault is selected only there.
Container clients verify Secure/HttpOnly/SameSite/path and cookie roundtrip. The
suite preserves historical collection failure on shutdown instead of treating
that expected nonzero result as a clean shutdown. See the release ledger for
actual commands, failed attempts, source/image receipts and remaining gates.
