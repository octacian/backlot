<!-- Generated from docs/containers.md; run make skills-generate. -->

## State compatibility

The durable state format is version 11; version 9 is the minimum supported baseline. Pre-v9
formats, including 6 and 8, are rejected unchanged; no migration from those
preproduction formats is provided. From v9 onward, retain every real Go migration
so the newest binary can apply the complete path when users skip releases,
without intermediate binaries or reset. The 9-to-10 step adds four empty shared
project/path/service/allocation buckets without rewriting retained records. The
10-to-11 semantic fence adds optional build-image pins/provenance; historical
generations keep no pins and no invented image identity.

Source validation, transformations, current authority validation and the final
version fence share one write transaction. Any precommit error, cancellation or
interruption rolls back the complete path. Preserve records and passive metadata
outside each step's explicit transformation; opening state does not touch retained
data, saved gateway configuration or CA identity. Recovery follows a committed
upgrade separately and never restarts workloads automatically.

Stop the old daemon normally before upgrading, wait for its exit and lock release,
then explicitly start the new binary with `daemon start --state-dir PATH` on the
same directory. Client status and shutdown responses require matching API/state
versions; use the matching binary for an incompatible daemon's lifecycle. A stopping
acknowledgment does not prove process exit or lock release. Startup keeps its normal
locking, without automatic retry/start or live process handover. Cleanup and
transport failures remain errors. Supported upgrades require no wipe or fresh directory.

Existing snapshots remain readable for recorded cleanup without becoming a current
input source or acquiring invented provenance. Eligible prepared/stopped starts use
current captured configuration after retained-resource/sensitive-value checks;
unsafe drift fails before effects. Failed/interrupted instances need explicit restart.

Preserve unsupported state for diagnosis and its matching binary's lifecycle.
A different directory does not clean its resources. Backlot never automatically
deletes incompatible state. See the [migration decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md).

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
instance ID. Destroy stops work and removes owned data, then erases snapshots, current results,
retained attempts, logs and artifacts, keeping a minimal `destroyed` identity. A later run gets a new
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
tradeoff is recorded in the [cleanup policy decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261019T023642577Z-authorize-owned-directory-cleanup-despite-unknown-foreign-binds.md).

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
joined before deletion. Logs remain after ordinary stop and disposable cleanup; successful explicit destroy
erases them. Export before destroy to retain caller-owned diagnostics.
Application output is captured as emitted and can contain application-printed
credentials; ordinary Backlot plans/status never expose private secret values.
See [disposable evidence](evidence.md) for artifacts, keep-on-failure, explicit
secret-access endpoints and age/size retention.

## Project-scoped shared services

Shared MariaDB and PostgreSQL servers retain independent instance allocations.

Declare `shared_services` and select `kind: allocation` resources per the
[manifest guide](manifest.md#shared-service-declarations); follow the runnable
[MariaDB](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/shared-mariadb/README.md) and
[PostgreSQL](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/shared-postgresql/README.md) examples for worktrees and lifecycle.
Root credentials stay in private provisioning state and the owned server's Engine
configuration, with no public API reference. Application passwords are sensitive:
inspect through named fixtures and explicit `secret` access, or select persistent
[startup details](daemon.md#declared-startup-details) to authorize their display.
Database/user names have no grant wildcards. Databases start empty; project jobs
own application schema.

The first selected start pins the local image ID and records server/storage intent
before readiness and allocation. Native consumers use 127.0.0.1 and a dynamic
loopback port; containers use the shared network alias and port 3306 (MariaDB) or
5432 (PostgreSQL). Administration stays private. Engine access is trusted.

MariaDB verifies identity and grant enforcement. PostgreSQL verifies identity,
SCRAM authentication and maintenance ACLs. Tested versions are MariaDB 11.4 and
PostgreSQL 17; no hard PostgreSQL major-version gate or automatic upgrade applies.
PostgreSQL requires the image entrypoint and `PGDATA=/var/lib/postgresql/data` on
the retained volume. Disable `PQGO_DEBUG=1` before starting the daemon: selected
PostgreSQL work refuses protocol diagnostics captured at process initialization.

PostgreSQL applications can create objects, named schemas and trusted extensions/
publications inside their allocation, without database ownership, grant option,
role membership or server administration. PUBLIC cannot connect to maintenance or
peer databases. Catalog metadata remains visible; shared SQL provides neither
tenant containment nor resource isolation.

Unexpected configuration, credentials, privileges or maintenance witnesses preserve
state. Restore the original configuration/authority or follow the explicit
retirement procedure below; there is no marker-reset command. Release does not
cascade or terminate SQL sessions. The
[PostgreSQL decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261025T202152388Z-add-bundled-postgresql-shared-allocations.md)
defines SQL ownership and recovery. Use a matching binary for retained PostgreSQL
records. PostgreSQL adds no persisted fields or migration; follow the current
[state compatibility policy](containers.md#state-compatibility).

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

The [decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261019T050443877Z-add-project-scoped-shared-service-allocations.md)
defines durable intent, SQL ownership and recovery. Missing acknowledged allocations
are never silently recreated; interrupted release cannot reopen as provisioning.
For uncertain effects/unavailable SQL, restore access and retry explicit reset/destroy.
Release preserves shared storage; shutdown/recovery joins consumers before servers,
retains data/credentials and requires explicit execution restart. Ordinary cleanup
requires ownership proof; the bounded force exception follows.

### Explicit operator force cleanup

Ownership uncertainty is preserved by default, including automatic disposable
cleanup and daemon recovery. To authorize deletion of the selected recorded shared
target despite uncertain proof, use the existing destroy command with `--force`:

```sh
backlot destroy --force --state-dir /absolute/private/daemon --json INSTANCE_ID
backlot shared destroy --force --state-dir /absolute/private/daemon --json SERVICE_ID
```

The flag is the acknowledgment; no second prompt is required. Instance force may
drop its recorded database and application user despite ownership-witness,
authentication or grant mismatch. PostgreSQL also drops the derived NOLOGIN owner
role; its ownership witnesses are role comments, while MariaDB uses a database
comment. Shared force may remove its recorded server/container, volume and network
despite capability-label mismatch. This can delete an externally replaced object
at a recorded handle; Docker volume identities are names. Neither command accepts
caller-selected SQL names or provider handles.
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
joined, resolve that first or restore access. Manual deletion of exact SQL
allocation objects can also be reconciled by force while the original server
is usable.

An unreadable, lost or corrupt state DB cannot supply a validated CLI target. Force
does not repair/reconstruct it. Retain a private backup and diagnostic inventory;
stop the daemon/consumers through retained authenticated authorities or their owning
supervisor; identify exact effects from trustworthy operator records, delete only
selected effects through provider/SQL administration and verify absence. If target
inventory is unavailable, investigate manually; do not scan/prune or signal observed
PIDs. A fresh state directory requires explicit accounting for old leftovers and
cannot truthfully mark unknown old obligations released. Never publish credentials,
capabilities, SQL statements or raw private state in recovery reports.

The bundled path validates the provisional private [`backlot.manager/v1` messages](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/api/v1/manager.go),
bounded to 64 KiB. The [manager contract](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261019T050443877Z-add-project-scoped-shared-service-allocations.md#decision)
separates ownership/effect from exit status and requires transient release-only
force or explicit unsupported failure. Custom execution remains later work.
