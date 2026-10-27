# Shared PostgreSQL allocations

Two scenes and linked Git worktrees use one project-owned PostgreSQL 17 server,
with a separate database and restricted application user per instance. The small
`visit` job uses the image's SQL client to create a table, insert a row and print
its row count in an application-owned schema. The application has database-local
CREATE, including named schemas and trusted extensions, without database ownership
or server administration. There is no extra application dependency to install.

Build Backlot with `make build` at the repository root and put `bin/backlot` on
your PATH. Docker must be running with a local Unix socket. Pull the image
explicitly before starting; Backlot never pulls or builds images:

```sh
docker pull postgres:17
cd examples/shared-postgresql
demo=$(mktemp -d /tmp/backlot-shared.XXXXXX)
chmod 700 "$demo"
```

Create `$demo/machine.yaml` with your Docker endpoint (for Docker Desktop on
macOS, commonly `unix:///Users/YOUR_USER/.docker/run/docker.sock`):

```yaml
version: backlot/v1
docker:
  endpoint: unix:///var/run/docker.sock
```

Use the same private daemon and machine configuration for every command below.
Planning is offline. Save the instance IDs returned by the two starts as
`DEV_ID` and `PREVIEW_ID` in your shell:

```sh
backlot plan dev --project "$PWD" --config "$demo/machine.yaml" --json
backlot daemon start --state-dir "$demo/state"
backlot run dev --project "$PWD" --config "$demo/machine.yaml" --state-dir "$demo/state"
backlot run preview --project "$PWD" --config "$demo/machine.yaml" --state-dir "$demo/state"
backlot logs --state-dir "$demo/state" --component visit "$DEV_ID"
backlot logs --state-dir "$demo/state" --component visit "$PREVIEW_ID"
backlot shared list --state-dir "$demo/state" --json
```

Both counts start at `1`; the shared list contains one server with two retained
allocations. These persistent scenes finish their job and retain their data.
Repeat `run dev` to join the existing instance without inserting again. Stop/start
and restart retain data; reset replaces only the selected allocation:

```sh
backlot stop --state-dir "$demo/state" "$DEV_ID"
backlot run dev --project "$PWD" --config "$demo/machine.yaml" --state-dir "$demo/state"
backlot restart --state-dir "$demo/state" "$DEV_ID"
backlot logs --state-dir "$demo/state" --component visit "$DEV_ID"
backlot reset --state-dir "$demo/state" "$DEV_ID"
backlot logs --state-dir "$demo/state" --component visit "$DEV_ID"
```

The latest job count is `3` after restart and `1` after reset. Logs include earlier
attempts. Preview still has its original row. The job receives only application
connection values, with its password supplied through `PGPASSWORD`; it never
prints credentials or receives the server's administrative credentials.

To try another linked checkout, run `git worktree add --detach /absolute/peer HEAD`
from the repository, then run `dev` with `--project /absolute/peer/examples/shared-postgresql`
and the same `--config` and `--state-dir`. Save its ID as `PEER_ID`. Its count starts
at `1`, and the same server now has three allocations. Restarting it increments
only its count. Independent clones have separate shared servers; matching project
names alone do not share. Keep declarations identical across linked worktrees.

Destroy each instance, including any peer, before destroying the shared service.
Copy `SERVICE_ID` from `shared list` into your shell. Service destruction refuses
retained allocations, even after scene stop:

```sh
backlot destroy --state-dir "$demo/state" "$DEV_ID"
backlot destroy --state-dir "$demo/state" "$PREVIEW_ID"
# If you started the peer:
backlot destroy --state-dir "$demo/state" "$PEER_ID"
backlot shared destroy --state-dir "$demo/state" "$SERVICE_ID"
backlot shared list --state-dir "$demo/state" --json
backlot daemon stop --state-dir "$demo/state"
```

The final shared list is empty. Remove the optional Git worktree with
`git worktree remove /absolute/peer`. Keep the private `$demo` directory if you
need historical logs; remove that exact directory only after successful cleanup.
The cached PostgreSQL image remains. No gateway, DNS or trust changes are needed.

See [shared-service declarations](../../docs/manifest.md#shared-service-declarations)
and [lifecycle and cleanup](../../docs/containers.md#project-scoped-shared-services).
This example covers PostgreSQL 17; custom-command managers remain a followup.
