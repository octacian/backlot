---
title: 'Compose machine configuration with registered private project inputs'
date: '2026-10-21'
status: proposed
summary: 'Register private project inputs and tool overrides outside checkouts so linked worktrees reuse explicit setup while machine provider settings remain shared.'
tags: [configuration, planning, security, identity, cli, persistence]
supersedes: []
superseded_by: []
---

# Compose machine configuration with registered private project inputs

Add one per-user project registry and a private inputs/tools document selected by
an explicit association. `backlot project setup` establishes that association
once; ordinary scene commands reuse it across linked worktrees. Compose private
inputs/tools over the existing machine configuration by key, validating both
sources before merging. Keep providers exclusively in machine configuration.

This proposal extends the offline configuration contract without changing its
environment ordering or runtime ownership. It is architecture for
[issue 48](https://github.com/octacian/backlot/issues/48), pending review and
acceptance; it does not claim implementation.

## Context

A common machine file cannot contain credentials for unrelated projects: the
planner correctly rejects inputs and tool overrides absent from the current
manifest. Generating merged machine files duplicates provider settings and
requires callers to remember file flags. Private files inside individual
worktrees also create copying and accidental-commit risks.

The approved outcome is explicit one-time setup, automatic reuse only within the
same local repository/project, strict validation even for overridden settings,
and consistent offline/runtime provenance. Names and remotes cannot authorize
credential reuse. Current `internal/plan` reads one machine file and validates
its declaration names; the daemon retains resolved private snapshots and compares
source digests. Runtime checkout identity deliberately distinguishes worktrees;
that identity must not become the credential-sharing key.

Environment sets already remove repetition in committed mappings. Literal seed
files suffice for existing project-relative application environment data when
worktree copying and file management are acceptable. Neither mechanism supplies
an external private project association, tool overrides, or shared provider
composition. They remain supported without application-file rewriting.

## Decision

### Association and selection

Use the existing per-user config root (`os.UserConfigDir()/backlot`), independent
of the daemon state directory. Store a versioned `projects.json` registry there,
with private documents beneath `projects/`. Reject a managed config root inside
the selected checkout; redirected test config roots must also be external.
Registry entries bind a canonical Git
common-directory path and its filesystem identity to the manifest path relative
to the checkout root. Resolve `.git` indirection and Git's `commondir` metadata,
including relative paths, with bounded filesystem reads; no Git command, remote
lookup, project-name matching or repository modification is required. Reject
malformed/unavailable Git metadata rather than treating it as a non-Git project.
For non-Git projects bind canonical project directory plus filesystem identity
and manifest filename. Reject a manifest alias escaping its checkout boundary.

Compare both canonical path and filesystem identity. Symlink aliases converge;
linked worktrees share the common directory but retain separate runtime checkout
identities and outputs. Different manifest-relative paths isolate projects in a
monorepo. Independent clones, including identical names/remotes, do not match.
Lookup detects the same recorded directory identity at a different path as moved,
and the same recorded path with a different identity as replaced; fail with an
explicit setup remediation, never reuse the old private document. A wholly new
clone is simply unregistered. A moved linked checkout whose common directory
remains unchanged may reuse configuration; this does not relax the daemon's
existing moved-checkout rejection. Moving/renaming the manifest creates a new
association, without inheriting credentials.

Only setup enrolls a project. An unregistered project retains existing
machine-only behavior. A registered missing, unsafe, invalid or unreadable
private file is an error, including for a scene using no inputs; never fall back
to machine values. A missing registry means no registrations; malformed or unsafe
existing registry is an error. Scene commands never create registry/private files.

Setup explicitly imports a private document from a file or stdin into managed
storage; it is not an ongoing dependency on the import file. Ordinary scene
commands select private configuration only through the registry. There is no
checkout-local discovery, per-operation private-file override, include chain or
additional merge level.

### Contracts and composition

Add optional input `description` and `generate: random-hex-32` to the committed
manifest. Generation is permitted only for secret inputs and means exactly 32
cryptographically random bytes encoded as 64 lowercase hex characters. Reject
unknown generation kinds. A declaration is a hint, never permission to generate
at plan/run time. Existing `{}` and `{secret: true}` declarations remain valid.
Existing manifest tools remain the executable requirements and defaults.

Define a named `api/v1` private configuration document with only `version`,
`inputs` and `tools`, using `version: backlot/v1`, YAML or JSON and existing
bounded strict decoding rules. No Docker, Caddy, storage or extension fields are
accepted. Keep registry persistence internal, with its own explicit format
version 1; reject unsupported versions without modification. Generate editor
schemas from the shared models during implementation.

Load manifest, selected machine file and selected private file once per resolution.
Validate every supplied layer independently, including all declaration names,
scalar shapes, NUL checks and unused provider settings. An overridden invalid
machine entry still fails. Then merge inputs and tools by key, private winning;
there is no deletion marker, recursive merge or interpolation. Preserve provider
settings and current machine/default selection unchanged. Empty input values
remain structurally valid but do not satisfy required presence; they cannot
silently fall back to a lower layer. Tool syntax validation covers all entries;
executable availability and input presence are required only for selected work,
including command readiness tools and input references in environments, images
and fixtures. Missing selected names instruct the caller to run project setup.

Relative private tool paths resolve from the current project directory, just as
existing manifest/machine tool paths do. Bare names use PATH; absolute external
executables are supported. Do not resolve relative tools beside the private file.
Do not execute tools or inspect providers during setup/offline planning.

### Setup and inspection

`backlot project setup` uses the same discovery, declaration validation and
composition functions as planning. On first setup, visit every declared input in
stable name order, showing its description and current configured/missing status.
Allow keeping a valid existing machine value; require an effective nonempty value
for every declared input to complete setup. Secret custom entry uses terminal
input with echo disabled and restored on success, cancellation and errors.
Generation requires an explicit acceptance for that input; rejecting it allows
custom entry or cancellation. Keep generated/custom values in memory until the
whole candidate passes validation. Setup lists tool defaults/overrides and allows
explicit overrides, without making unavailable tools for unselected scenes block
configuration storage. It installs no tools/providers and changes no app files.

For an already registered project, ordinary setup is inspection: report association,
exact private document path, and configured/missing input/tool names, then exit
unchanged. New declarations appear as missing without triggering writes/prompts.
Explicit edit options opt into an update and preserving existing values; do not
regenerate or rotate on inspection. Explicit reassociation is required after a
move/replacement and must not import old credentials implicitly.

Noninteractive setup accepts one bounded structured private configuration from a
file or stdin, plus explicit generation requests naming declared eligible inputs.
Never accept secret values in command arguments. Supplied input entries update
by key over current private entries; unspecified entries stay unchanged. Reject
an input supplied both as a literal and a generation request. Validate the whole
candidate, including effective completeness for all inputs and machine settings,
before committing anything. Missing answers, invalid data, generation failure,
EOF or cancellation leave the association and active private file unchanged.
Non-TTY calls without structured input/edit intent inspect existing setup or fail
with actionable guidance for first setup; they never wait on prompts. JSON mode
emits one named shared result/error envelope, with prompts confined to a real
interactive terminal. Generation requests in noninteractive mode are explicit
acceptance; hints alone never generate.

Ordinary inspection exposes status/names, not input values. Only
`project setup --dangerously-reveal-secrets` explicitly adds configured secret
values to the setup inspection result, human or JSON; reject reveal combined with
mutation options. Keep this local inspection boundary separate from daemon
plan/run/doctor/export contracts: the flag does not flow into scene requests or
relax redaction elsewhere. No reveal in prompts, error messages or logs.
Non-secret input values need not be printed by ordinary setup either. Paths,
declaration descriptions and tool paths are not secret channels; callers must
keep credentials out of them, consistent with existing manifest guidance.

### Atomic publication and concurrent access

Use one stable per-user registry lock file, with the existing `gofrs/flock`
dependency, to serialize setup commits across processes. Do not hold it while
prompting. Capture the registry revision, relevant source digests and association
identity before collecting changes; under the lock reread and compare them before
commit. Conflicts fail with retry guidance rather than overwriting another
writer's edits. Recheck source identity before accepting a read snapshot as well.
Manual editors are outside the advisory lock protocol; detect changed source
bytes before commit, and document that operators must not edit during setup.

Publish a new uniquely named immutable private document for each successful
update. Create it exclusively with private permissions, write/flush/close it and
sync its containing directory before referencing it. Then write a complete new
registry to an exclusive sibling temporary file, flush/close it and atomically
rename it over the old registry, syncing the directory. That registry replacement
is the single visibility/commit point for both enrollment and updates. Readers
read one complete registry generation and its immutable document, so concurrent
setup cannot pair new association metadata with old values. No two mutable-file
transaction or write-ahead recovery subsystem is needed. Do not modify machine
configuration or the import file.

Before the commit point, failure leaves the active association/document unchanged;
a new unreferenced private file may remain after a crash. After replacement, a
sync/response failure can leave a committed result with uncertain durability:
report that outcome explicitly and instruct inspection, never claim rollback.
Retain old generations so readers already holding an old registry can finish.
Remove ordinary temporary files on handled failures; do not run a collector or
background recovery. Explain that retained generations contain old secrets and
require explicit operator deletion when no readers need them. Rotation does not
promise erasure from private generations, daemon snapshots, backups or memory.

Require owner-only directories (0700) and regular owner-only files (0600) on the
supported Unix platforms; reject symlinks and unsafe existing ownership/permissions
at managed registry/private boundaries without repairing them. Do not impose these new
checks on legacy machine files or require managed-file permissions on transient
setup import sources; imported values are written only into private storage. Use the repository's platform privacy/identity
adapters rather than treating Unix mode bits as Windows support. Local filesystem
atomic rename/locking is required; network/roaming configuration is outside scope.
This preserves the existing trusted-user boundary, not protection against hostile
code already executing as the same user.

### Provenance, drift and compatibility

Use one composition path for offline plan, daemon prepare/run/restart/reset and
selected-scene diagnostics; adapters must not perform their own merge. Persist
private launch provenance: selected association and identity, private source path,
present/absent state, source digests and effective configuration
digest. An absent registration is distinct from historical unrecorded provenance.
Do not put raw input maps or registry contents in public scene results.

Keep `PlanResponse.config_digest` as the existing machine-file byte digest for
compatibility, adding an effective configuration digest for composed behavior.
For machine-only resolution, preserve existing drift behavior. For composed
resolution derive a deterministic, domain-separated digest from validated effective
inputs/tools/providers, including path-resolved tools, rather than concatenated
ambiguous file bytes. Keep per-source digests privately for current-file diagnosis:
a formatting-only edit or an overridden value change is source drift, while an
effective change is configuration drift. Public digests remain local provenance
metadata and are not safe secret fingerprints to publish indiscriminately.

Daemon preparation and launch must use the same captured source generation;
remove any double-resolution opportunity that could compare one generation and
launch another. Persist resolved sensitive values in existing private snapshots.
A running attempt stays immutable when setup changes; stop/cleanup/recovery use
recorded snapshots and never require today's registry or private file. Explicit
restart/reset resolves current layers and applies existing retained-contract and
pre-stop drift safety checks. Diagnostics show launch versus current sources,
missing/rebound associations and effective drift without replacing launch values.
Run-export launch provenance must use the attempt's effective digest as well as
its existing machine digest, without disclosing private source contents.

Keep the `v1` API namespace and existing machine/manifest documents valid; new
optional fields are additive, and older strict binaries reject new manifest
fields rather than silently interpreting them. Advance daemon StateVersion from
5 to 6 when composition ships: older binaries would ignore new association/drift
provenance and could restart with incomplete configuration. Use existing rejection
without migration; an operator with v5 state must use the matching binary to
clean up/shut down owned resources before choosing a fresh state directory.
The project registry remains independent and is not erased by daemon reset or
state-directory replacement. No state version changes in this architecture-only
proposal. Do not retrofit old snapshots with guessed private provenance.

## Consequences

One explicit setup supports linked worktrees and multiple isolated projects while
provider settings stay shared. Machine-only users need no enrollment. Strict
validation prevents a private override from hiding an invalid lower layer.
The cost is a small synchronous configuration store, terminal UX, shared model/
schema changes, extra snapshot provenance and explicit state-version transition.

The concrete implementation scope is:

| Area | Obligations and boundary |
| --- | --- |
| Persistence | One registry, stable advisory lock, immutable private generations; existing daemon snapshots gain source/effective provenance and state v6. No additional database. |
| Writers | Setup alone writes registry/private documents; existing daemon snapshot/attempt/export writers record composed provenance. Read-only consumers never enroll or repair. |
| APIs | Named private/setup models, effective digest fields in shared v1 contracts; no setup/reveal daemon endpoint. Existing scene APIs consume the same resolver. |
| Background work | None added: no watchers, retries, secret refresh, generation pruning or new startup workers. Existing daemon owners and retention remain unchanged. |
| Dependencies | Reuse strict YAML/JSON parser, crypto/rand, filesystem/privacy adapters and gofrs/flock; hidden terminal input may require the maintained Go terminal package when implemented. No provider SDK or secret-manager dependency. |
| Operator work | Explicit initial setup/edits/reassociation, private-file backups and deliberate old-generation removal; existing provider/trust installation remains separate. State v5 cleanup uses its matching binary. |
| Shared infrastructure | Machine selection/providers and daemon lifecycle remain shared as today; no machine-file rewriting, provider installation, DNS/trust change or application config editing. |
| Unused behavior | Resolution performs bounded registry/identity reads even for machine-only projects, but creates no files/locks or background tasks. Absent registry preserves machine-only operation; an invalid existing registry fails explicitly. |

Implementation must demonstrate two projects sharing provider settings with
independent inputs, two real linked worktrees, independent clone/same-name
isolation, moves/replacements, strict overridden validation, selected-scene
requirements, project-relative tools, real TTY echo restoration/generation
acceptance/cancel, JSON/redaction/reveal isolation, concurrent setup conflicts,
pre/post-commit failures, private provenance/drift and immutable restart behavior.
These are behavior gates, not evidence already obtained by this proposal.

## Alternatives considered

An explicit private-file flag alone is a smaller first increment but does not meet
the approved ordinary-run and worktree-reuse outcome. Use setup import to select the document once. Checkout-local ignored files simplify
lookup but require copying and increase accidental credential exposure. Project
name/remote matching enrolls unrelated clones and cannot establish authority.

One mutable file holding the entire registry and every project's secrets makes
atomic writes simpler, but cannot provide an independently selected inputs/tools
document or meaningful registered-missing-file behavior. Two mutable files with
rollback/journaling add recovery complexity; immutable documents plus one registry
commit provide coherent readers with a deliberate retained-generation cost.
Using the daemon database for setup would force offline planning and initial
configuration to depend on daemon availability and state migrations. A second
configuration database adds an engine without demonstrated query needs.

Keeping source-byte drift alone is simple but conflates source formatting with
effective changes and cannot describe masked overrides. A deep-merge/include or
secret-manager subsystem adds lifecycle and precedence choices the problem does
not require. Existing environments/seeds remain preferable when external private
association and tool overrides are unnecessary.

## Revisit when

Revisit if retained private generations create an observed operational burden,
network/roaming config storage becomes required, same-user adversarial isolation
is required, or native Windows implementation cannot meet atomic publication and
privacy through its existing platform adapters. A concrete need for additional
private provider fields or remote secret refresh requires a new scope decision;
do not prebuild those mechanisms.

## References

- [Issue 48](https://github.com/octacian/backlot/issues/48)
- [Offline contract ADR](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
- [Daemon lifecycle ADR](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Shared API ADR](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Windows privacy/identity ADR](20261018T210000000Z-propose-windows-private-ipc-and-filesystem-identity.md)
- [Manifest and environment guide](../manifest.md)
- [Architecture](../architecture.md)
- [Shared manifest models](../../api/v1/manifest.go)
- [Planner](../../internal/plan/plan.go), [private snapshot](../../internal/plan/snapshot.go)
- [Checkout identity](../../internal/daemon/identity.go), [state version enforcement](../../internal/daemon/state.go)
- [Runtime launch/restart](../../internal/daemon/runtime.go), [diagnostics](../../internal/daemon/doctor.go)
