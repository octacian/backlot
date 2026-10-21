# Containers and retained private state

Windows Docker Desktop Linux-container integration is planned in #38; see the
[Windows handoff](windows-design.md#docker-desktop-and-managed-https-design-38).
Named-pipe endpoints and Windows mount identities are not implemented here.

Milestone 4 runs generic containers through the official Docker Go client, alongside
native commands. It adds private volumes, directories, secrets and networks,
fresh-only initialization, retained restart, reset and destroy. Metadata `prepare`
still validates and records a snapshot without contacting Docker or allocating
provider resources. HTTPS publication follows the [gateway guide](https.md).

## Run the MariaDB fixture

Use a local Docker engine and existing Docker tooling to pull or build images.
Backlot never builds images or pulls them implicitly. A native each-start build job
may invoke the project's declared Docker/build tool before a dependent container.
Provider absence affects selected Docker work, while metadata and native-only
startup do not contact Docker. If an engine is configured, private-directory
cleanup retains that endpoint and verifies its consumers even for native-only
scenes. An unavailable configured engine blocks destructive directory cleanup.

```sh
make build
docker pull mariadb:11.4
docker pull alpine:3.21
# Copy examples/private-state/machine.example.yaml outside the checkout and set
# its endpoint to your local engine's unix socket; Docker contexts can show it:
docker context inspect --format '{{.Endpoints.docker.Host}}'
bin/backlot daemon start --state-dir /absolute/private/backlot --json
bin/backlot run dev --project examples/private-state --config /absolute/machine.yaml \
  --state-dir /absolute/private/backlot --json
bin/backlot stop --state-dir /absolute/private/backlot --json INSTANCE_ID
bin/backlot restart INSTANCE_ID --state-dir /absolute/private/backlot --json
bin/backlot reset INSTANCE_ID --state-dir /absolute/private/backlot --json
bin/backlot destroy --state-dir /absolute/private/backlot --json INSTANCE_ID
bin/backlot logs --state-dir /absolute/private/backlot --json INSTANCE_ID
```

`run`/`restart`/`reset` take flags after the target. `stop`/`destroy`/`logs` take
flags before the recorded ID. Reset and restart load the recorded project/config
paths by default; explicit `--config` may select a new machine file. Destroy uses
only recorded ownership, so a missing or changed checkout cannot redirect cleanup.

The fixture image owns database initialization through `MARIADB_*` environment
settings. Backlot has no SQL provisioning, shared database, migrations or seed
logic. Applications own those operations and any application-level readiness
probe. A TCP probe observes connectivity, which may precede application setup;
use a declared native command probe when connectivity is insufficient.

## State compatibility

The durable state format is version 6. A complete supported v5 layout transitions
additively without changing records, keys or retained files; older binaries then
reject v6 before interpreting explicit native waivers. Other formats, including v4,
are rejected unchanged, including malformed v5 layouts. Use the matching older
binary to clean up owned instances and stop its gateway/daemon before selecting
a fresh `--state-dir` with the new binary. Preserve old state until cleanup is
verified; a new directory does not clean up resources from the old one. Backlot
never automatically deletes incompatible state.

## Generations, jobs and drift

Each accepted execution records an attempt. Each retained generation records its
own resource capabilities and generated secrets in the private daemon database.
Containers are recreated on start, with Docker restart policies disabled. Storage,
network and secret generations survive persistent stop/run, restart and daemon
shutdown. Generated credentials remain paired with their storage. Host port values
are scoped to an attempt and bound on loopback by default; explicit machine
`docker.publish_address` selects another numeric interface. Containers use service
references for private DNS names and internal ports. Container-to-native references
require explicit `docker.host_address` and reachable native listener interfaces;
see the [HTTPS/mixed guide](https.md). Managed HTTPS consumers on
macOS ARM64 Docker Desktop and the [bounded rootful Linux topology](platforms.md)
explicitly declare origin `host_mappings` and read-only
`origin_ca_cert` mounts. The explicit `host: host-gateway` Engine token expands
at each container create, without helper images or copied IP addresses.
`ca_cert_path` resolves to the chosen container file target in explicit
application environment assignments; it never exposes the host path or signing
keys. These declarations participate in conservative retained-contract drift
checks. See [container application trust](https.md#public-root-file-mounts-for-containers).


A fresh-only job is skipped only after its successful exit and completion record.
A failed initialization leaves its generation incomplete. Explicit restart can
retry that same contract/generation; no background retry, repair or initialization
rollback occurs. Each-start jobs run at every start. Image initialization is the
image's responsibility and does not create a separate Backlot job record.

Repeated run returns the recorded instance and manifest/config digest drift.
Restart applies ordinary commands, probes and non-sensitive environment changes.
Before stopping any existing work, it rejects retained resource declarations,
attachments/mounts, storage-consuming container image/argument changes, fresh-only
contracts, Docker endpoint changes (including port-only scenes with an implicit
network), and resolved sensitive/inherited/seed-value changes. Implicit native
launch locations such as PATH/HOME/TMPDIR may change for ordinary work; explicit
pass-throughs, seeds and secret assignments remain protected even for those keys.
Fresh-only contracts protect their resolved launch values too. Mark credentials `secret: true` or use declared secret inputs/resources.
The conservative contract may require reset for changes that an application could
handle; there is no permissive compatibility engine. Failed/interrupted instances
require explicit restart. Stop/run reuses the old snapshot, reporting drift.

Reset stops and joins owned consumers, removes the old generation, accepts the new
validated snapshot and initializes/starts a new generation under the same logical
instance ID. Destroy stops work and removes owned data, retaining snapshots,
results and historical logs under a `destroyed` status. A later run gets a new
instance ID. Repeated destroy is idempotent. Cleanup failures return nonzero and
retain remaining handles for explicit cleanup retry. Neither reset nor destroy
promises atomic rollback after destructive cleanup has begun.

Checkout output concurrency groups lock the declared checkout/group for the
entire execution, including live services that can write those outputs. Independent
worktrees use different groups. Uncertain owned consumers keep groups blocked.
Changed snapshots cannot rename a group to bypass an active/uncertain consumer
of an overlapping path; stop that consumer first. Container output directories
are created inside the project filesystem boundary and rechecked before mounting.
Backlot cannot infer undeclared writes or side effects delegated to other engines.

## Ownership and recovery

Intent is committed before Docker create and capability labels precede activation.
Actual engine IDs are recorded before exposing readiness. Cleanup verifies each
recorded label/token, including name lookup for an interrupted create intent.
Directory cleanup requires a private directory and its matching ownership marker;
missing markers, replaced/symlinked paths and mismatched Docker labels are preserved
with diagnostics. Native group cleanup must finish before retained data is removed.
Docker consumers of volumes/directories/networks also block resource deletion,
including unrelated consumers; no broad prune or forced volume/network deletion
is used. Image-declared anonymous volumes belong to the owned container and are
removed with it.

Authenticated directory ownership authorizes cleanup by default even when a
foreign bind source is unresolved, dangling, noncanonical or otherwise uncertain.
Inspection still blocks positively identified same/ancestor/descendant consumers,
including resolved symlink and case aliases, in running and stopped declarations.
Unknown identity is not proof that a bind is unrelated: an unidentifiable foreign
alias may consume the directory and lose its backing data during cleanup. Do not
rely on uncertain foreign binds to retain Backlot-owned data. This intentional
tradeoff is recorded in the [cleanup policy decision](adr/20261019T023642577Z-authorize-owned-directory-cleanup-despite-unknown-foreign-binds.md).

Docker Desktop can report a VM-prefixed source such as `/host_mnt/private/...`.
On native macOS, the adapter verifies the configured local Engine reports Docker
Desktop/Linux before reconciling this exact clean prefix to an existing resolved
host counterpart. Every viable raw and verified translated identity is compared,
so a real host `/host_mnt` path cannot hide a consumed directory and a failed
candidate cannot hide a known overlap through another. Unavailable namespace
verification leaves translation unknown; raw host overlaps still block deletion.
A known consumer produces a preservation diagnostic; remove it before retrying
reset/destroy. Backlot never stops or removes it implicitly. A verifiably unrelated
peer can continue running while another instance resets or destroys.

This policy applies only to inspected foreign metadata. Ordinary project mount
inputs and their private-state fences retain strict host filesystem validation.
Docker list/inspect errors still block cleanup, as do owned-consumer shutdown,
directory ownership/privacy, replaced/symlinked path and volume/network safeguards.

After a daemon crash, verifiably owned surviving containers are stopped, available
logs are retained, and containers are removed. Data/credentials remain retained;
instances become interrupted and require explicit restart. Unknown ownership stays
untouched. An unfinished intent with a verifiably owned effect can be recorded on explicit
restart; a missing or unverifiable retained allocation requires reset/destroy
rather than silently creating empty storage. Recovery records a
collection gap, because output/status around the interruption may be incomplete.

Docker log drivers may transform output before returning it; Backlot preserves
the bytes supplied by the engine. Container stdout/stderr is collected outside Docker containers while running and
joined before deletion. Historical `logs` remains available after destroy.
Application output is captured as emitted and can contain application-printed
credentials; ordinary Backlot plans/status never expose private secret values.
See [disposable evidence](evidence.md) for artifacts, keep-on-failure, explicit
secret-access endpoints and age/size retention.

## Shared API

The existing named `RunRequest`/`InstanceResponse` contracts serve `/v1/run`,
`/v1/restart` and `/v1/reset`; restart/reset require `instance_id`. Named
`StopRequest`/`InstanceResponse` serve `/v1/runtime/stop`; force requires the
selected `expected_attempt`. `InstanceRequest`/`InstanceResponse` remain the
contracts for `/v1/destroy`.
The CLI/client and server share these Go types. Logs use the existing typed
paged/NDJSON contracts. Resource capabilities and credential values stay private.

See [testing/tooling](testing.md) for the provider integration command and resource
requirements. A passing local suite is milestone evidence, not all-V1 acceptance.
