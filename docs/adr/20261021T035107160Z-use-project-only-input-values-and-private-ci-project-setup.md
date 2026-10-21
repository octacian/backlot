---
title: 'Use project-only input values and private CI project setup'
date: '2026-10-21'
status: superseded
summary: 'Supply input values exclusively through registered private project configuration and reuse atomic CLI setup inside an isolated CI job registry.'
tags: [configuration, security, ci, cli, persistence, compatibility]
supersedes: [20261021T033508474Z-fence-combined-configuration-and-native-recovery-state-at-version-seven.md]
superseded_by: [20261021T040930738Z-select-a-private-project-registry-without-redirecting-application-configuration.md]
---

# Use project-only input values and private CI project setup

The committed project manifest declares inputs; registered private project
configuration is their sole value source. Remove machine inputs entirely and
provide optional structured project setup through the existing setup/quickrun
Actions. Keep providers, storage, HTTPS and executable overrides machine-configurable,
and private executable overrides project-relative. Protect action-owned setup and
inspection transport and private configuration files while retaining declaration-based
public redaction and application responsibility for printed/written content. Use
daemon state version 8, rejecting all other formats without migration.

This replacement for the [combined version-seven decision](20261021T033508474Z-fence-combined-configuration-and-native-recovery-state-at-version-seven.md)
changes only input sourcing, CI setup and the compatibility fence. It retains
that decision's unaffected registered-configuration and native-recovery guarantees
through its referenced chain. Independent review and maintainer acceptance retain the combined decision as the
immediate superseded record without rewriting historical rationale.

## Context

The previous registered configuration design merged private inputs over machine
inputs. That leaves project credentials in a shared machine document, lets removal
expose a lower-layer credential, and requires machine declaration membership to
vary by project. The approved pre-0.1 amendment deliberately removes that fallback,
including documents with empty or unused machine `inputs`. Existing Actions own
an external private job root and daemon/gateway cleanup, but have no structured
project setup and currently inherit the operator's user config selection.

The intended outcome is explicit project enrollment and isolated CI inputs without
an extra caller file or CLI step. Installation-only setup must remain possible
without a project manifest. No automatic copying, migration, input-sharing shim,
new runner platform or changes to native recovery authority are requested. The
approved output boundary covers action-owned configuration handling and private
material; it does not require filtering arbitrary application content or making
previously public input references secret.

## Decision

### Sole project input source

Remove `MachineConfig.Inputs` from the public model, machine schema, validation,
load and fallback paths. Strict decoding rejects the presence of machine `inputs`
in YAML or JSON, including `{}`, null and unused entries; no option accepts the
old shape. Keep `backlot/v1` document and `v1` API namespaces: this is an explicit
breaking pre-0.1 contract, not an additive compatibility claim.

Use a separate internal resolved configuration type holding effective inputs,
tools and provider settings. Do not retain a hidden input surrogate in public
`MachineConfig`, or construct one to validate private input values. Validate input
and tool scalar/name rules through the existing manifest/private validation
boundary independently of machine provider validation. Load each selected source
once, validate each independently, then compose tools by key with private winning.
Effective inputs are exactly the registered private input map. An invalid machine
tool/provider setting still fails even when unused or overridden. A private
configuration cannot provide provider/storage/HTTPS settings.

An unregistered project has no input values. It can plan/run a scene whose selected
work needs none, with valid machine configuration and tools; selected missing
inputs give project-setup guidance. Only explicit setup enrolls. Existing registered
missing, invalid, unsafe or unreadable private configuration remains an error even
for an input-free scene. Setup completion requires every declared input to have a
nonempty private value. Ordinary resolution requires presence only for selected
work; private empty values never enable fallback. Removing a private input exposes
no machine value and fails the complete candidate if that input is still declared.
Tool removal may still expose a machine override or manifest default.

Retain the full association and publication contract from the [registered
configuration decision](20261021T001732173Z-compose-machine-configuration-with-registered-private-project-inputs.md):
Git common-directory plus filesystem identity and manifest-relative association;
non-Git identity; linked-worktree reuse with separate runtime identities;
clone/monorepo isolation; move/replacement/reassociation fencing; external managed
storage with strict ownership/mode checks; registry format 1; stable advisory lock;
immutable generations with one registry commit point, conflict detection and
explicit uncertain-commit reporting. Keep atomic explicit removals/retirement
repair, no implicit pruning, bounded file/stdin imports, no secret arguments,
hidden terminal entry, explicit generation and inspection-only local reveal.
Generation remains `random-hex-32`, only for eligible secret declarations, with no
plan/run-time generation. Private relative executable paths use the selected
project directory, not the private file's directory.

Retain one captured resolution for daemon preparation/launch, private launch
snapshots, source and effective digests, source-versus-effective drift distinction,
launch/current diagnostics and public redaction. `config_digest` remains the machine
byte digest; effective configuration covers private inputs, effective path-resolved
tools and providers using deterministic domain separation. Do not publish raw
maps/registry contents. Running attempts remain immutable; stop/cleanup/recovery
use recorded snapshots independently of current registry validity. Restart/reset
resolve current values and retain existing pre-stop drift fences. Export retains
effective provenance and native ownership warnings without secret disclosure.

### Action integration and secret handling

Add optional `project` (default `.`), `project-inputs` (strict JSON object of string
values) and `project-generate-inputs` (explicit JSON array of input names) to setup;
quickrun exposes the same options and passes them through its shared setup path.
Leaving both structured options unset (including their empty runner defaults)
skips enrollment and manifest discovery, even when a project target is present.
Explicit `{}` or `[]` requests setup, supporting an input-free registration. A
nonempty option must contain the required JSON shape; whitespace alone is invalid.
Reject duplicate keys/names, invalid identifiers, non-string or empty/NUL input
values, unknown names and ineligible/overlapping generation requests;
validate a complete candidate before any registry publication. The action's JSON
adapter must preserve strict duplicate/type checks, rather than relying on
`JSON.parse`'s last-key behavior. Manifest membership and completeness remain CLI
setup responsibilities, not a second planner inside the action.

Resolve the project path once. Spawn the installed CLI directly, without a shell,
as `project setup --project PATH --config MACHINE --from - --json`, adding explicit
`--generate NAME` arguments for requested names. Send a bounded
`{version: "backlot/v1", inputs: ...}` JSON document over stdin and close it.
Extend the existing bounded child-command helper to support stdin; values must
never enter argv. No new secret file supplied by the caller, daemon endpoint,
setup protocol or secret-output contract is needed. Use the existing CLI's atomic
setup update and sanitized diagnostics; never emit its raw captured streams on
secret-sensitive failures.

Mask all supplied values before validation errors, subprocess work or other logging
could expose them. Escape workflow-command metacharacters/newlines when registering
masks. Parse failures report fixed structural diagnostics without input snippets or
raw exception text. Keep input payload environment variables out of spawned build,
Git, Caddy and Backlot process environments after capture; do not pass them to
workloads or write them to job environment/state/output files. Action-owned setup,
inspection and error transport must not emit raw supplied or generated values.
Masking is a log backstop; these sensitive paths must suppress raw values even
when masks have been registered.

For explicit generation, let CLI setup generate and commit the values. Before
starting the daemon/gateway or any scene, use the existing local inspection-only
`project setup --dangerously-reveal-secrets --json` in a captured, bounded pipe to
obtain generated secret values, register masks and discard the captured values.
Do not log/return raw inspection output or raw setup/inspection errors; fail closed
before runtime work if capture or masking fails. The setup and inspection calls
are the necessary secret-handling boundary, with sanitized failures throughout;
there is no runtime work between generation and masking. This reuses the local
explicit reveal contract without forwarding reveal into daemon requests or adding
public secret outputs. Never read private persistence through an action-specific
schema fallback. Require action/runtime revisions supporting this contract; an
incompatible pin fails rather than silently skipping project setup.

### Job-private configuration and lifetime

Under the authenticated mode-0700 job root, create a private config-home directory
outside every checkout. For supported Ubuntu runners only, set `XDG_CONFIG_HOME`
to that directory in a shared Backlot-process environment helper, so the existing
`os.UserConfigDir()/backlot` store selects the job's registry/generations. Keep the
explicit generated job machine config path. Do not write a job-global
`XDG_CONFIG_HOME`/`HOME` through `GITHUB_ENV`, change operator config, or redirect
Git, Go, application Node, browser, Caddy or SDK configuration. Derive the path
from the verified owner in subsequent run/helper/post invocations; persisted
job metadata contains paths/ownership only, never input values.

Apply that environment to every Backlot child: project setup/inspection, daemon
start (and inherited daemon resolution), gateway operations, plan, run, diagnostics,
list/inspect/export, destroy and cleanup including failure/post paths. The direct
run spawn must use it too; adding it only to the generic command helper is
insufficient. Keep daemon-only explicit trust settings and existing application
trust mappings. Configuration redirection must not become workload configuration.

Register cleanup ownership before allocations. When requested, complete project
setup and masking after installation and machine-config creation, before daemon
start; installation-only setup retains its existing private runtime behavior
without reading a manifest. Failure before daemon attempt can remove the owned
root; after attempt, authenticated bounded cleanup must finish or preserve uncertain
state. Successful cleanup deletes the job registry and every private generation
with the owned root, after consumers stop. Public audited evidence remains outside
that root and readable after cleanup. Never publish private payload, registry,
generation, snapshot or capability files through outputs, caches or artifacts,
or log their raw contents.

Preserve existing declaration/reference-based public redaction: secret input
references remain redacted, while non-secret input references may remain public
literals in plans and provenance. Applications remain responsible for values they
print or write into their logs, reports and artifacts, including generated secrets.
The existing evidence safety audit and workflow masks do not sanitize arbitrary
application file contents. This amendment adds no arbitrary input-content filtering
or new public-value redaction contract. Retained uncertain roots are a reported
cleanup failure, not a promise of secret erasure after runner destruction.
Keep the current ephemeral Ubuntu 24.04 amd64 runner restriction.

### Compatibility and retained recovery

Advance shared daemon `StateVersion` from 7 to 8. Reject version 7, 6, 5 and every
other unsupported/corrupt format before recovery or retained-resource mutation,
leaving database bytes and retained files unchanged. Recheck running-daemon state
compatibility at each configuration-sensitive client mutation, including
prepare/run/restart/reset, so an already-running v7 daemon cannot consume the old
input semantics. No snapshot retrofit or automatic registry/machine-input copy
is permitted. The independently versioned private registry remains format 1;
existing valid private project configurations need no conversion.

Operators use the matching old binary to clean up and shut down old owned
resources, preserving old state until verified, then choose fresh daemon state.
A fresh directory does not clean old resources. Operators explicitly remove
machine `inputs` and supply project values through setup themselves. Old private
generations still contain old secrets until explicitly deleted when readers no
longer need them; rotation does not promise erasure from snapshots/backups/memory.

Retain all unaffected native recovery, boot provenance, authenticated cleanup,
force-stop waiver, collection-gap, original-exit, deletion guard and saved gateway
configuration/CA guarantees from the combined decision and its native-recovery
chain. This amendment introduces no process census, new authority, migration,
automatic recovery worker, secret collector or background generation task.

## Consequences

Input ownership becomes explicit and project-isolated, at the cost of deliberately
rejecting previously valid machine documents and v7 daemon state. Existing fixtures,
examples, provider/acceptance callers and generated schemas must move input values
to explicit private setup; docs must remove machine-input and fallback guidance.
The runtime and adapter changes touch shared resolution and action process
boundaries, so independent architecture review precedes substantial implementation.

The storage/locking/retention burden remains the existing synchronous registry;
CI adds an owned directory and bounded setup/inspection subprocesses, no new
service or dependency. Input-free projects remain usable without enrollment.
Actions installation without structured setup does not scan manifests or touch
the default registry. CI private generations have the existing job-root lifetime;
local generations keep their explicit operator retention obligation. The action
protects its configuration transport and private files; application evidence keeps
its existing public-value/redaction and content-responsibility boundary. Exact-head
Ubuntu action success/failure/cleanup and isolation evidence, early real CLI/native
runtime evidence, focused tests and full applicable regressions are implementation
gates, not evidence supplied by this record.

## Alternatives considered

Keeping machine fallback with private overrides retains ambient cross-project
credentials and removal ambiguity, contrary to the approved sole-source choice.
An automatic machine import, compatibility switch or v7 migration conceals the
breaking boundary and needs new provenance/transition policy; explicit setup and
fresh daemon state are the selected pre-0.1 tradeoff.

Requiring a caller-authored file and separate setup command can use the current
CLI, but fails the approved reusable-action experience. Action-side registry writes
would duplicate safety/publication semantics; direct CLI stdin reuses them. Having
the action generate secrets itself would duplicate manifest eligibility/generation
rules; captured local inspection provides masking through an existing explicit
boundary. Printing reveal output and trusting masks alone is insufficient.

Filtering every occurrence of supplied/generated values from public plans, logs
and artifacts would change public-value semantics and add a separate content
policy, including encoded/transformed application output. That broader obligation
is outside the approved boundary; retain declared secret redaction and application
responsibility rather than introducing an exact-string scrubber or a new filter.

A job-global config-home override is simpler but redirects unrelated tools and
operator selection. A new product-specific config-root flag/environment variable
would expand every adapter's API unnecessarily while scoped existing Linux config
selection meets the bounded runner requirement. A secret service, additional merge
layer or collector adds persistence and ongoing work without a required consumer.

## Revisit when

A stable released contract requires migration, supported Actions runners expand,
or a demonstrated non-CI consumer requires an explicit registry-root API. Revisit
mask capture if bounded local inspection cannot meet the no-output boundary;
do not silently expose secrets or add a compatibility fallback.

## References

- [Issue 48](https://github.com/octacian/backlot/issues/48)
- [Combined compatibility decision](20261021T033508474Z-fence-combined-configuration-and-native-recovery-state-at-version-seven.md)
- [Registered configuration guarantees](20261021T001732173Z-compose-machine-configuration-with-registered-private-project-inputs.md)
- [Native recovery guarantees](20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
- [Offline strict decoding contract](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
- [Configuration resolution](../../internal/plan/configuration.go)
- [Atomic setup](../../internal/plan/setup.go)
- [Registry storage](../../internal/projectconfig/store.go)
- [Local CLI setup](../../internal/cli/project.go)
- [Private shared contracts](../../api/v1/project.go)
- [Action setup](../../actions/setup/setup.mjs)
- [Action runtime/environment and cleanup](../../actions/setup/runtime.mjs)
- [Action direct invocation](../../actions/setup/invocation.mjs)
- [Action collection helper](../../actions/setup/job.mjs)
- [Quickrun](../../actions/quickrun/quickrun.mjs)
- [Public value resolution](../../internal/plan/plan.go)
- [Evidence safety audit](../../internal/evidenceaudit/audit.go)
- [Application evidence responsibility](../../internal/daemon/export_html.go)
- [CI guide](../ci.md)
