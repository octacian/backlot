# Native Go tests with disposable MariaDB

This adopter recipe runs native `go test` against an instance-owned MariaDB
container, volume, generated credentials and dynamic loopback port. The image
initializes `app` and its restricted application user. Backlot owns lifecycle;
application code owns queries, schema and test data.

The repository already includes `go-sql-driver/mysql` 1.9.3. When copying these
files into your own Go module, declare the driver there. The native readiness
command uses that same installed driver, receives credentials only through
environment mappings and emits no connection error text. It authenticates as
`app` against `app`, then queries the selected database and its table metadata.
An open TCP listener or a successful administrative login cannot satisfy it.

Build Backlot from the repository root with `make build`. Outside Backlot,
explicitly pull the chosen image, then copy `machine.example.yaml` to a private
absolute path and set its Docker Unix endpoint (Docker Desktop commonly uses a
different socket). The adopter recipe selects `mariadb:11.4.13`. The same bounded
database check has run against 10.11.19, 11.4.13, 11.8.9 and 12.3.3 on macOS
ARM64; fresh Ubuntu CI image-pull and runner cost acceptance remains pending.

Choose a new private absolute `STATE`, an existing private `EVIDENCE` directory,
the private machine file as `CONFIG`, and the built binary's absolute `BACKLOT`:

```sh
docker pull mariadb:11.4.13
cd examples/native-go-mariadb
go build -o bin/ready ./cmd/ready
"$BACKLOT" daemon start --state-dir "$STATE" --json
"$BACKLOT" run test --project "$PWD" --config "$CONFIG" --state-dir "$STATE" --json -- -run '^TestDatabase$' -v
```

Build the probe before planning/running: declared tool availability is checked
up front. The compiled helper does not need Go's module/cache environment during
container readiness; it receives only the explicitly mapped connection values.

`go test` receives the arguments following `--` as individual arguments, without
shell reconstruction. The test creates a table and verifies application write/read
access. Each disposable invocation starts with fresh storage. Go's JSON test
events remain in retained native logs; `reports/result.json` records the original
test exit when the process finishes normally, including assertion failures.
Cancellation may interrupt report writing; it cannot promise a completed report.
See [native runner cancellation](../../docs/evidence.md#native-runner-cancellation)
for the distinction between client signals, native results and partial evidence.
Keep credentials out of application log messages and test names.

Save the returned instance ID as `ID`. After completion, publish an audited
portable bundle, then destroy through the authenticated daemon:

```sh
"$BACKLOT" export "$ID" --output "$EVIDENCE/$ID" --wait=45s --state-dir "$STATE" --json
"$BACKLOT" destroy --state-dir "$STATE" --json "$ID"
"$BACKLOT" daemon stop --state-dir "$STATE" --json
```

Check execution, collection and cleanup failures separately from the terminal
exit. Retain private fixture state when cleanup is uncertain and retry using its
recorded ID; never prune Docker or remove resources by observed name/PID. Base
images remain available. Do not upload daemon state, environment or credentials.

The readiness helper and native test are application code to adapt. Bad-password,
cancellation, evidence and multi-version verification belong in the repository's
separate verification suite, rather than the adopter's command path. For retained
development data, see [private state](../../docs/containers.md); shared server
provisioning is a [different recipe](../shared-mariadb/README.md).
