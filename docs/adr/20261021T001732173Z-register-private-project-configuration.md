---
title: 'Register private project configuration'
date: '2026-10-21'
status: accepted
summary: 'Register project-only private inputs and tool overrides outside checkouts, reuse them across linked worktrees, and isolate CI setup without redirecting application configuration.'
tags: [configuration, security, identity, cli, persistence, ci, compatibility]
supersedes: [20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
superseded_by: []
---

# Register private project configuration

The committed project manifest declares inputs; registered private project
configuration is their sole value source. `backlot project setup` explicitly
registers external private inputs and tool overrides once, and ordinary scene
commands reuse that association across linked worktrees. Keep provider, storage,
HTTPS and shared executable settings in machine configuration. Setup and quickrun
Actions reuse the setup CLI through stdin and select a job-private registry
without changing application HOME or XDG configuration.

Use daemon state version 8 for the combined private-configuration and native
recovery contract, with a one-way in-place transition only from the previous merged
main state version 6. This record replaces only the compatibility/transition policy of the published native recovery decision;
retain all its boot provenance, authenticated ownership, explicit force-stop waiver,
collection-gap, original-exit, deletion-guard and saved gateway/CA guarantees.

## Context

Shared machine input values create cross-project credential collisions. Private
files inside worktrees require copying and risk accidental commits; a private-file
flag on every scene command makes routine execution fragile. The required boundary
is explicit initial setup followed by automatic reuse within the same local
repository/project, with no enrollment by project name or remote URL.

Offline planning must work without a daemon. Linked worktrees share credentials
but keep distinct runtime checkout identities and outputs. CI needs supplied or
explicitly generated inputs during reusable-action setup, without secret arguments,
a separate caller-authored file, or changes to unrelated tool/application config.
This is a deliberate pre-0.1 breaking contract: no machine-input fallback, import
shim, migration of input values, new runner platform or secret-manager service.
Preserving retained daemon state across the supported predecessor upgrade is a
separate requirement from enrolling project configuration.

## Decision

### Association, storage and selection

Use a per-user registry independent of daemon state: `projects.json` and immutable
private documents beneath `projects/`. The default is
`os.UserConfigDir()/backlot`. Optional `BACKLOT_PROJECT_CONFIG_DIR` selects that
direct registry directory, not a parent config-home folder. An explicit empty,
relative, invalid or unsafe selection fails without default fallback. Use one
selection helper for storage and diagnostic/provenance paths. Reject managed
storage inside the selected checkout; resolve ancestors and reject final-component
symlinks and unsafe managed ownership/modes. Validate existing explicitly selected
roots even without a registry; preserve the ordinary provider-only default-directory
behavior when no registry exists. Read-only lookup never creates managed storage.

Associate Git projects by canonical common-directory path, filesystem identity
and manifest path relative to the checkout root. Resolve bounded `.git` indirection
and `commondir` metadata without a Git subprocess or remote lookup. Invalid or
unavailable Git metadata is an error, not a non-Git fallback. For non-Git projects,
use canonical project directory, filesystem identity and manifest filename. Reject
manifest aliases escaping the checkout boundary.

Symlink aliases converge and linked worktrees reuse the association; independent
clones and separate manifest-relative projects remain isolated. Never match by
project name or remote. Detect recorded directory identity at a changed path as
moved, and a recorded path with changed identity as replaced; fail with explicit
setup remediation rather than inheriting credentials. A wholly new clone or moved
manifest is unregistered. Moving a linked checkout may retain this association
when its common directory is unchanged, without relaxing runtime checkout fences.

Only explicit setup enrolls. Missing registry means no registrations; an unsafe or
malformed existing registry fails. An unregistered scene needing no inputs can run
with valid machine settings/tools. Selected missing inputs produce setup guidance.
A registered missing, invalid, unsafe or unreadable private document is always an
error, even for an input-free scene. Imports copy values into managed storage;
there is no ongoing import-file dependency, checkout discovery, per-run private
path override, include chain or extra merge layer. Registry format remains 1.

### Input and tool contracts

Remove machine `inputs` from the public model, schema, validation and resolver.
Strict YAML/JSON decoding rejects the field even when empty, null or unused.
Keep the pre-0.1 `backlot/v1` documents and `v1` API namespaces without claiming
old machine-input documents remain compatible.

The named private document accepts only `version`, `inputs` and `tools`, with
bounded strict decoding and existing name/scalar/NUL/tool rules. No private
provider/storage/HTTPS settings are accepted. Load each selected source once and
validate it independently, including unused or overridden machine entries. Use
an internal resolved type rather than hiding private inputs in `MachineConfig`.
Effective inputs are exactly the private map; tools merge by key with private
winning over machine/default settings. No recursive merge, interpolation or
input fallback is added. Tool removal may expose a machine override or default.

Resolve relative private tools from the current project directory, not the private
file's directory. Bare names use PATH; absolute external tools remain supported.
Validate all entries, but require input presence and executable availability only
for selected work, including readiness/environment/image/fixture references.
Setup/offline planning do not install tools or inspect providers.

Manifest input declarations may include `description` and `generate: random-hex-32`.
Only secret inputs may request that hint: 32 cryptographically random bytes,
encoded as 64 lowercase hex characters. Unknown generation kinds fail. A hint
never authorizes generation during plan/run. Existing environment sets and literal
seed files retain their ordering and do not rewrite application files.

### Setup, inspection and atomic edits

First interactive setup visits all declared inputs in stable order, showing
requirements and descriptions. Every declared input needs a nonempty private value
to complete setup. Secret custom entry hides echo and restores it on success,
error and cancellation. Generated suggestions require acceptance; rejecting one
allows custom entry or cancellation. List tool defaults/overrides and allow
explicit overrides without requiring executables for unselected scenes.

Ordinary setup on an existing registration inspects without prompting or writing:
report association, exact private path and configured/missing names, with values
redacted. New declarations appear missing. Explicit edit intent preserves
unspecified values. Noninteractive setup accepts one bounded private document
from file/stdin and explicit generation requests, never secret command arguments.
Non-TTY calls without update intent inspect or fail with guidance, never wait
for prompts. JSON uses shared typed results/errors; prompts use a real terminal.

Explicit `--remove-input`/`--remove-tool` edits remove named private entries.
Reject duplicate/nonexistent/conflicting removals and supplied/generated overlaps.
Retired declarations may be repaired atomically: strictly read the current private
document, deferring only current-manifest membership, apply removals/replacements
in memory, then validate every remaining entry and all declared input values.
Unsafe/malformed private data and invalid machine settings cannot be hidden or
repaired this way. No implicit pruning, renaming or deletion markers. Removing a
still-required input fails without publishing; combined rename repairs cannot
publish only their removal. Shared worktrees must all satisfy the resulting
strict declarations. Reassociation after move/replacement is explicit and never
implicitly imports old credentials.

Only local inspection with `--dangerously-reveal-secrets` reveals configured secret
values; reject combining reveal with edits. It never flows into daemon requests
or relaxes plan/run/doctor/export redaction. Keep credentials out of names,
descriptions and tool paths. Invalid/incomplete updates, EOF, rejected answers,
failed generation and cancellation leave the active association/generation intact.

### Publication, privacy and retention

Serialize setup commits with one stable advisory registry lock using the existing
flock dependency; never hold it while prompting. Capture revision, source digests
and association identity, then reread/compare under lock before commit. Recheck
identity for read snapshots too. Conflicts fail with retry guidance; manual edits
are outside the lock protocol and must not race setup.

Write/flush/close an exclusive immutable private generation and sync its directory.
Write a complete registry to an exclusive sibling temporary file, flush/close,
atomically replace the registry and sync its directory. Registry replacement is
the single commit/visibility point; readers pair one registry with its immutable
generation. Before replacement failure preserves active state, though crashes may
leave an unreferenced generation. After replacement a sync/response failure may
mean committed-but-uncertain durability; report that outcome, never promise rollback.
Clean handled temporary files; retain old generations for in-flight readers.

Managed directories are owner-only 0700 and regular files 0600 on supported Unix
platforms, with symlink/ownership checks and no automatic repair. Do not impose
these checks on legacy machine files or transient import files. Require local
filesystem atomic rename/locking; no network/roaming or native Windows guarantee
is introduced. This is a trusted-user boundary, not same-user hostile-code isolation.

No collector, background repair, refresh worker or automatic pruning is added.
Operators delete obsolete generations only after readers finish. Rotation does
not erase old secrets from generations, snapshots, backups or memory. Daemon reset
or a fresh state directory does not erase or migrate the independent registry.

### Captured resolution and provenance

Use one composition path for offline planning, preparation, launch, restart/reset
and selected diagnostics. Preparation/launch consume one captured generation;
never compare one resolution and launch another. Keep sensitive resolved values
and source/association identity privately in snapshots. Running attempts remain
immutable after edits; recorded stop/cleanup/recovery work independently of current
files. Restart/reset resolve current values and retain pre-stop drift fences.

Keep `config_digest` as the machine-byte digest and add deterministic,
domain-separated effective configuration provenance over validated private inputs,
resolved tools and providers. Track private per-source digests separately: formatting
or overridden-source changes are source drift, not necessarily effective drift.
Distinguish absent registration from historical unrecorded provenance. Diagnostics
show launch/current selection and drift without replacing launch values; export
retains effective provenance and native ownership warnings. Never publish raw input
maps or registry contents; digests are not universally safe secret fingerprints.

### CI integration and disclosure boundary

Setup and quickrun accept optional `project` (default `.`), `project-inputs` (strict
JSON string-value object) and `project-generate-inputs` (explicit JSON name array).
Unset structured options skip enrollment/manifest discovery; explicit `{}`/`[]`
requests setup. Reject duplicate keys/names, wrong types, empty/NUL values, invalid
identifiers and overlapping/ineligible generation requests. CLI setup retains
manifest membership and complete-candidate validation; no second planner is added.

Spawn the existing CLI directly without a shell, pass bounded structured values
over stdin to `project setup --from - --json`, and use explicit named generation
requests. Values never enter argv, job outputs/state/env files or raw error messages.
Mask supplied values before potentially exposing work, escape workflow mask syntax,
and remove raw payload variables from all child environments. Parse failures and
sensitive subprocess failures use bounded sanitized diagnostics, not captured streams.
For generated values, capture the existing local reveal command, register masks
and discard values before any runtime work; failure to inspect/mask fails closed.

Before configuration resolution, including installation-only setup, check existing
`version --json` metadata for expected API and `state_version: 8`. Unsupported pins
fail before consulting the operator registry. Preserve independent source-SHA and
machine/daemon checks; no capability service or compatibility fallback is added.

Select a job-private external registry beneath the authenticated 0700 job root
through `BACKLOT_PROJECT_CONFIG_DIR` on every verified-owner Backlot child:
setup/inspection, daemon/direct run, gateway, plan, diagnostics/export and post
cleanup. The launch baseline excludes the selector; preserve explicit application
pass-through rules without a new forbidden-variable policy. Leave HOME/XDG,
machine-file selection and unrelated Git/Go/Node/browser/Caddy/SDK environments
unchanged, including manifest-selected HOME/XDG pass-through.

Register cleanup before allocation. Finish requested setup/masking before daemon
start; installation-only setup still needs no manifest. Stop/join consumers before
deleting the registry and all private generations with the owned root. Preserve
uncertain cleanup as failure. Audited public evidence remains outside that root;
never publish payload/registry/generation/snapshot/capability files. Protect
configuration transport and private files; masks are only a log backstop.
Declaration-based redaction stays intact, public non-secret input literals may
remain visible, and applications remain responsible for content they print/write.
No arbitrary input-content filtering or new secret-output contract is added.
Keep the existing ephemeral Ubuntu 24.04 amd64 Actions support boundary.

### State compatibility and native recovery

Support exactly the previous merged main daemon state version 6 to final version
8, in place and one way. Intermediate version 7 was exercised by development
binaries; retaining 8 prevents those binaries from accepting final project-only
input semantics. Version 5, version 7, newer/other versions and corrupt supported
state fail unchanged before recovery or retained-resource mutation. No v5/v7
migration, compatibility mode or accumulating migration chain is introduced.
Main's existing additive v5-to-v6 transition is context, not a second transition
retained by this binary. Fresh state initializes at 8; valid version 8 state reopens
normally.

Under exclusive daemon/database ownership, validate the complete v6 layout and
known persisted records in the same bbolt write transaction that advances the
version. Reuse existing typed persistence decoders and check only the concrete
identity/index/parent links needed to avoid misaddressed records or lost ownership
obligations. This is not an exhaustive lifecycle or snapshot/manifest graph audit;
never validate historical records against current sources or repair old digests. Respect legitimate
interrupted intent/effect states and historical missing boot/provenance fields;
missing authority remains unknown rather than fabricated. Perform no provider,
process, registry or retained-file effects during migration. The only persisted
change is the format fence: preserve all other keys and record bytes, including
unknown metadata, capabilities, private values, histories, saved configuration and
CA/data/evidence files. Validation or pre-commit failure rolls back the whole
transaction. A committed fence is one-way even if subsequent daemon startup fails;
normal recovery then uses the existing lifecycle and its failure reporting.

Keep existing snapshots and resolved private values readable for recorded
inspection, redaction, comparison and authorized cleanup/recovery. The actual v6
snapshot stores resolved plan/secrets and provider configuration, not an embedded
machine input document; its missing private-source/effective provenance remains
unrecorded, never inferred as absent registration or reconstructed from current
files. Do not rewrite historical digests or invent launch/boot facts. Any legacy
read representation needed for a recorded field stays private to persistence and
cannot be accepted as current machine configuration or input resolution.

Before any new attempt from a migrated prepared or stopped snapshot, capture
current project-only configuration and apply existing retained-resource and
sensitive-value compatibility checks before stopping work, replacing snapshots or
allocating effects. Ordinary start/run must not discard that capture and execute
legacy resolved values. Compatible attempts can retain logical identity and data;
unsafe drift fails before mutation under existing restart/reset rules, without
making reset/destroy a migration prerequisite. Failed/interrupted instances still
require explicit restart. Record new source/effective provenance only with the
new accepted snapshot/attempt, committed together in the existing execution
acceptance transaction so rejected acceptance preserves historical provenance.
Current machine documents containing `inputs`
remain invalid; explicit project setup supplies current values, without automatic
import, enrollment or fallback. Input-free scenes retain their ordinary setup rules.

Check running-daemon compatibility afresh before each configuration-sensitive
mutation, including named start and gateway start/restart. The new CLI may request
normal shutdown of the supported v6 daemon: accept an exact state6 `stopping`
acknowledgment only for `POST /v1/stop`, retaining its truthful version and all
normal typed/API/header/content/body/PID/lease/error validation. Status and
configuration-consuming preflights remain strict state8; v5/v7/newer acknowledgments
are unsupported. Preserve cleanup, transport and cancellation failures. No generic
older-version mode, normalization, retry/polling, auto-restart, signaling or new
endpoint is introduced. A stopping acknowledgment is not proof of final process
exit or lock release. Once normal shutdown finishes, start the new binary on the
same state directory, without a wipe or fresh directory. No live process handover
or automatic application restart is promised. Recorded reads/cleanup retain their supported
behavior. Gateway recovery/start preserves validated saved effective configuration
and CA identity under existing fences, without rereading historical machine inputs.

This replaces only the published native recovery record's transition policy;
retain all its boot/ownership/waiver/collection/exit/deletion and saved
configuration/CA guarantees. The supported upgrade must never require destroying
retained workloads/data or wiping state. Existing valid format-1 project registries
need no conversion. Unsupported formats remain preserved for diagnosis and their
matching binary's lifecycle; changing directories never cleans old resources.
Provider/trust installation remains separate.

## Consequences

One setup supports linked worktrees and isolated projects while provider settings
remain shared. Strict independent validation prevents overrides hiding invalid
sources. The bounded cost is a synchronous registry, immutable retained generations,
terminal UX, shared schema/provenance and action process-boundary handling; no new
service, database, provider SDK, application-file rewriting or background task.
Unused resolution performs bounded identity/registry reads but never enrolls.

The breaking input contract requires explicit operator setup, while supported
v6 state upgrades in place without data cleanup. Deliberate private-generation
retention/deletion remains an operator responsibility. Required validation covers
real worktree/clone/monorepo isolation, move/replacement fences, atomic concurrent
edits/removal repair and uncertain commits, TTY restoration, captured-generation
launch/drift, atomic v6-to-v8 validation/rollback and old-reader rejection,
legacy snapshot cleanup and current-only new attempts, old-daemon mutation
rejection and recorded cleanup, and current-head CI supplied/generated input transport, failure, cleanup and evidence lifetime.
Acceptance status is a decision, not a claim that those implementation gates ran.

## Alternatives considered

Per-command private-file flags and ignored checkout files require repeated selection
or copying; project-name/remote matching cannot authorize credential reuse. Machine
input fallback reintroduces ambient credentials and removal ambiguity. Automatic
input import invents credential association/provenance and conceals the breaking
boundary. Rejecting all v6 state forces unnecessary cleanup and risks retained data
when its existing snapshot/ownership shapes can be preserved. A migration chain or
compatibility mode adds historical writers and semantics beyond the single
supported main-to-feature transition; the direct transactional fence is sufficient.

A single mutable secret registry or two-file rollback journal adds exposure or
transaction machinery. Immutable generations plus one commit point provide coherent
readers with an explicit retention cost. A daemon database or secret service would
couple offline setup to a running service without a demonstrated need. Explicit
named removals avoid full-value resubmission and unsafe implicit pruning.

Child-scoped XDG redirects explicitly selected application values; job-global HOME/
XDG also redirects unrelated tools. A direct registry selector expresses the actual
boundary without restoring environment state across adapters. Caller-authored CI
files/steps undermine reusable setup; action-side registry writes or generation
duplicate CLI safety rules. Trusting workflow masks alone or filtering arbitrary
application output would exceed the selected transport/file boundary.

## Revisit when

A future merged predecessor needs a different transition, retained generations
become an observed burden, supported runners/platforms expand, local storage guarantees no
longer hold, or a demonstrated remote-secret/provider consumer requires new scope.
Reassess selection and disclosure explicitly instead of adding silent fallback.

## References

- [Issue 48](https://github.com/octacian/backlot/issues/48)
- [Native recovery guarantees](20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
- [Offline configuration contract](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
- [Manifest, setup and private configuration](../manifest.md)
- [CI setup and support boundary](../ci.md)
- [Registry identity, storage and atomic publication](../../internal/projectconfig/store.go)
- [Configuration resolution](../../internal/plan/configuration.go)
- [Atomic setup and repair](../../internal/plan/setup.go)
- [Captured snapshots](../../internal/plan/snapshot.go)
- [Shared state version](../../api/v1/daemon.go)
- [State opening](../../internal/daemon/state.go)
- [Client compatibility fences](../../internal/client/client.go)
- [Action setup and process cleanup](../../actions/setup/setup.mjs), [runtime](../../actions/setup/runtime.mjs)
