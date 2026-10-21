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
settings. This private-container path remains available alongside the optional
[shared-service manager](#project-scoped-shared-services-gate-1). Backlot does not
own application migrations or seed logic. Applications own those operations and any application-level readiness
probe. A TCP probe observes connectivity, which may precede application setup;
use a declared native command probe when connectivity is insufficient.

The durable state format is version 7. Older formats, including version 6, are
rejected without modification or migration because older binaries cannot
interpret the separate shared-service/allocation recovery journals. Use the matching older
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
`InstanceRequest`/`InstanceResponse` serve `/v1/runtime/stop` and `/v1/destroy`.
The CLI/client and server share these Go types. Logs use the existing typed
paged/NDJSON contracts. Resource capabilities and credential values stay private.

See [testing/tooling](testing.md) for the provider integration command and resource
requirements. A passing local suite is milestone evidence, not all-V1 acceptance.


## Project-scoped shared services (Gate 1)

This #43 slice implements a local MariaDB 11.4 path, with Gate 2 lifecycle work
in progress after Gate 1 acceptance. It does not
claim the full concurrency/crash/lifecycle acceptance matrix, PostgreSQL, custom
command execution, final examples or platform CI. See [implementation status](implementation-plan.md#shared-services-issue-43).

Declare `shared_services` and select `kind: allocation` resources as documented in
[manifest references](manifest.md#shared-service-declarations). Generated root
credentials stay only in private provisioning state and the owned server's Engine
configuration. The public API has no administrative-secret reference. Application
passwords are automatically sensitive; use named fixtures and existing explicit
`secret` access if inspection is needed. Database/user names contain no grant
wildcards. Fresh databases have no application schema; project jobs perform it.

The first selected start pins the existing local image ID, records volume/network/
server intent, then ensures readiness before allocating. Native connection values
use 127.0.0.1 and a dynamically published loopback port. Containers attach to a
separate owned shared network and receive its alias and port 3306. No host DNS,
extra gateway, runtime pull/build or widened bind is needed. The administrative
password never enters consumer environment. Engine access is trusted administration.
The bundled path checks MariaDB 11.4 and enabled grant enforcement before allocating.
Only the exercised MariaDB 11.4 image is evidence; this is not generic MySQL support.

List recorded services, then control the recorded service ID through the daemon:

```sh
backlot shared list --state-dir /absolute/private/daemon --json
backlot shared inspect --state-dir /absolute/private/daemon --json SERVICE_ID
backlot shared stop --state-dir /absolute/private/daemon --json SERVICE_ID
backlot shared start --state-dir /absolute/private/daemon --json SERVICE_ID
backlot shared restart --state-dir /absolute/private/daemon --json SERVICE_ID
backlot shared destroy --state-dir /absolute/private/daemon --json SERVICE_ID
```

List/inspect report durable lifecycle state, retained allocation counts and pending
release counts with bounded control-failure diagnostics; they are not SQL health
probes. Instance inspect also retains failed explicit cleanup diagnostics. Start also happens for selected scene dependencies.
Stop/restart refuse active/kept dependents. Destroy refuses every retained allocation;
explicitly destroy those instances first. Scene stop never stops the shared server.
Server declarations/endpoint must match retained configuration exactly; use distinct
names for incompatible servers. No automatic upgrades, relinking or external
adoption occur. Separate daemon state roots do not pool resources.

Allocation intent, names, ownership and passwords are durable before SQL effects.
Atomic database comments contain an ownership fingerprint, never the raw private
authority. User authentication and bounded grants are checked before reclaiming
partial effects. Missing acknowledged allocations are not silently recreated.
Release intent is durable before SQL deletion; ordinary start cannot reopen an
interrupted release as provisioning. Shared destruction intent survives individual
stop/removal observations and blocks start until explicit destruction completes.
Accepted shared-control requests are canceled and joined during daemon shutdown.
Uncertain identities/effects and unavailable SQL retain cleanup obligations and
return failure; restore the provider and retry explicit reset/destroy. Never delete
SQL objects or Docker resources by name. Shared storage/servers survive individual
allocation release. Shutdown/recovery joins consumers before stopping servers and
preserves data/credentials; execution requires explicit restart.

### Explicit operator force cleanup

Ownership uncertainty is preserved by default, including automatic disposable
cleanup and daemon recovery. To authorize deletion of the selected recorded shared
target despite uncertain proof, use the existing destroy command with `--force`:

```sh
backlot destroy --force --state-dir /absolute/private/daemon --json INSTANCE_ID
backlot shared destroy --force --state-dir /absolute/private/daemon --json SERVICE_ID
```

The flag is the acknowledgment; no second prompt is required. Instance force may
drop its recorded database and exact application user even when the database comment,
user authentication or grants no longer match. Shared force may remove its recorded
server/container, volume and network despite capability-label mismatch. This can
delete an externally replaced object at a recorded handle; Docker volume identities
are names. Neither command accepts caller-selected SQL names or provider handles.
Container/network effects with no acknowledged immutable ID still require original
capability recovery or manual exact removal, not adoption of a same-name replacement.

Both commands verify actual absence before settling durable cleanup obligations.
Unavailable provider/SQL access, failed deletion or unverified absence returns
failure and retains an inspectable obligation. Partial completion permits another
explicit attempt on the same recorded targets. Force authority is invocation-only:
it is not persisted or reused by recovery after a crash. An older daemon that
rejects the flag is an error, never a reason to retry silently without it.

Consumers must stop/join first. Native guardian and private directory/container
ownership checks remain enforced; force does not authorize PID/name-only signaling
or deletion beneath known consumers. Known overlapping retained SQL allocations
block deletion. Shared destruction still refuses **every retained allocation**,
including stopped instances and pending cleanup; explicitly destroy each instance
first. Force does not remove foreign consumers, force volume/network removal, or
implicitly remove volumes from drifted server mounts. Before SQL access, Backlot
still verifies the exact existing server's image/configuration, storage/network,
administration environment and current loopback binding. It creates no replacements
on this path. Restore original topology/admin access if those checks cannot pass.

A missing acknowledged server is not proof that SQL data is gone: its retained
volume may still hold every allocation. When both the exact recorded server container
and backing volume are authoritatively absent at the recorded provider, instance
force can reconcile that instance's SQL effects as absent without SQL access. Missing
network alone, missing server alone, a live server with missing volume, or provider
contact failure cannot establish this proof. Unselected peer obligations remain.

If missing infrastructure prevents SQL cleanup, either restore original topology
and administration manually, or deliberately retire that server's data. For retirement,
stop/join **all affected consumers** first; with trusted private operator inventory,
use provider tools to delete only that exact server container and backing volume,
and verify both absent. Then force-destroy each affected recorded instance ID
individually, followed by shared-destroy of the service ID to remove its remaining
recorded network and metadata. This is destructive whole-server retirement decided
by the operator, not an implicit instance-cleanup cascade. If consumers cannot be
joined, resolve that first or restore access. Manual deletion of only an exact SQL
database/user can also be reconciled by force while the original server is usable.

An unreadable, lost or corrupt state DB cannot supply a validated CLI target. Force
does not repair/reconstruct it. Retain a private backup and diagnostic inventory;
stop the daemon/consumers through retained authenticated authorities or their owning
supervisor; identify exact effects from trustworthy operator records, delete only
selected effects through provider/SQL administration and verify absence. If target
inventory is unavailable, investigate manually; do not scan/prune or signal observed
PIDs. A fresh state directory requires explicit accounting for old leftovers and
cannot truthfully mark unknown old obligations released. Never publish credentials,
capabilities, SQL statements or raw private state in recovery reports.

See the [force-cleanup decision](adr/20261020T233406000Z-allow-explicit-force-cleanup-of-recorded-shared-targets.md)
for the bounded exception and retained guarantees.

The initial private `backlot.manager/v1` protocol reads one JSON stdin request and
writes one JSON stdout result, bounded to 64 KiB per document. Requests carry the
operation, durable allocation ID/ownership and generated allocation values. Results
bind the same ID and report ownership (`owned`, `absent`, `uncertain`) separately
from effect (`ready`, `partial`, `released`); success exit alone proves nothing.
Backlot owns cancellation/deadlines/serialization and persists partial/unknown
obligations. Trusted project code must cooperate with inspect/reconcile/release;
unavailable/unverifiable code preserves obligations. Gate 1 validates message
contracts using the bundled MariaDB path; executable custom managers and the
non-Go non-database fixture arrive at Gate 3 and may refine these initial fields.

The private manager request's optional `force` boolean is valid only for `release`.
It is passed in a copy for an explicit operation and excluded from durable requests.
Managers must honor this bounded authorization or fail explicitly as unsupported;
exit0 alone cannot establish absence. Gate 3 custom execution must demonstrate this
contract; there is no capability registry or silent fallback to ordinary release.
