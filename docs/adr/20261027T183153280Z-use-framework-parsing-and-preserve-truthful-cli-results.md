---
title: 'Use framework parsing and preserve truthful CLI results'
date: '2026-10-27'
status: accepted
summary: 'Use urfave built-in parsing and existing named result contracts to make CLI invocation and failure interpretation predictable without another parser or response framework.'
tags: [cli, contracts, diagnostics, compatibility]
supersedes: []
superseded_by: []
---

# Use framework parsing and preserve truthful CLI results

Use the pinned urfave/cli v3.14.0 parser for every public command, with one global
`--json` flag and framework error hooks. Keep existing named JSON results and
NDJSON modes, application exits, lifecycle authority and disclosure rules. Make
failures actionable without discarding independently observed results or accepted
instance/attempt identity. No replacement parser, streaming project, styling pass
or universal response envelope is required.

This extends the accepted Go/typed-API decision. The owner approved the native
positional-tail grammar below on 2026-10-27. Accepted after independent
architecture review (no blocking or major findings) and coordinator approval on
2026-10-27; implementation verification remains separate.

## Context

[Issue 56](https://github.com/octacian/backlot/issues/56) reports inconsistent
invocation and error framing across developer and agent entry points. At base
`90df9aa`, plan, run/restart/reset and project setup bypass framework parsing;
other commands use it but omit consistent usage-error handling. Runtime options
include ignored `--project` on restart/reset. Common human rendering omits useful
execution details and has no shared-service result case. Several observation and
cleanup failures discard previously accepted identity.

The owner chose native parsing over malformed-input order independence and
explicitly excludes parser conformance tests. Existing finite failures can carry
valuable data: a failed run has its original exit, an unhealthy doctor has
findings, and a failed export can retain the settled application result. Replacing
these with a generic error or a universal success flag would lose information.

Source inspection of the pinned library confirms `OnUsageError`,
`ExitErrHandler`, persistent flags, `OnlyOnce` and native `--` consumption. It also
finds a compatibility limit: `Args()` combines positional values from both sides
of `--` and exposes no delimiter-position accessor. No new parser is justified
to recover information the library intentionally consumes.

## Decision

### Invocation and help

- Remove public `SkipFlagParsing`, manual flag loops and JSON pre-scans. Register
  supported flags on their actual commands, configure singleton flags with
  `OnlyOnce`, and retain explicitly repeatable setup flags. Let the framework
  handle option ordering, equals syntax and token consumption. Value validation
  and cross-option/lifetime rules remain Backlot responsibilities.
- Register `--json` once on the root with normal inheritance. Route framework
  usage failures through `OnUsageError` on every applicable command, including
  groups; configure error handling so the executable produces one diagnostic
  rather than framework/main duplicates. Reuse `PlanError`, `ErrorResponse` and
  `ExitError`; do not add an error framework.
- When the framework has already recognized JSON mode, usage/semantic failures
  emit a typed `invalid_arguments` error and no automatic human help on stdout.
  Otherwise clear stderr and nonzero exit suffice. Do not inspect raw arguments
  to discover JSON after a parse failure or promise order independence for
  malformed input. Valid invocations retain structured runtime errors.
- Reject options that cannot affect the selected operation before effects.
  Restart/reset take recorded IDs, derive project identity from the record and
  reject `--project`; `--allow-image-change` belongs to restart only. Preserve
  existing attachment, force, timeout and observation restrictions. No option
  may silently promise an effect the command ignores.
- Help describes each command's target, supported flags/defaults, examples,
  lifecycle effects and relationships: ID `status`/`inspect` inspect recorded
  state; named `scene status` resolves a persistent scene; follow observes;
  attachment owns an attempt. Metadata-only preparation is distinct from runtime
  readiness. Group invocations with no child remain help; unknown children fail.
- Explicit help, root/group help without an action, and `licenses` are human-text
  exceptions even when `--json` is recognized. Licenses retain canonical license
  bytes. Help success does not imply a lifecycle result. Invalid invocations
  outside explicit help still follow the usage-error rules.

**Owner-approved terminal argument grammar.** Preserve exact terminal values
after `--` using only library-parsed arguments: first positional is the scene;
the remaining values form `TerminalArgs`. Also accept extra plain positionals
without a separator, since the library does not distinguish them; `--` protects
flag-like terminal arguments. Thus `run dev extra` and `run dev -- extra` both
forward `extra`. The owner explicitly approved relaxing the old rejection of
unseparated extra positionals. A raw scan, reordered argv or replacement parser
is excluded. Persistent and attached lifetime validation still rejects unsupported
terminal arguments; restart/reset accept no terminal tail. Existing separated
invocations remain valid.

### Machine framing and error context

| Invocation | Stdout contract |
| --- | --- |
| Finite plan, version, setup, prepare, inspect/status, lifecycle, fixture/secret, doctor, list, gateway/shared and export actions | One existing named JSON response, or `ErrorResponse` if no usable result exists |
| `logs`, including a finite retained-log read | Existing `LogRecord` NDJSON; zero records is valid; failures emit one `ErrorResponse` line before or after log records |
| ID/named status with `--follow` | Existing `SceneFollowRecord` NDJSON |
| `run --attached` / `scene start --attached` | Existing `AttachedRecord` NDJSON |
| `daemon serve` | Foreground service; no successful JSON result is promised; failure uses the ordinary error path |
| Explicit help, root/group help and licenses | Human text as above |

No progress/result event is added to finite commands. NDJSON records retain their
existing types and completion/error rules; no new transport, replay or log
completeness guarantee is introduced. Diagnostics/warnings use stderr in human
mode, and existing typed fields where available in machine mode. Log warnings
and retention gaps retain their existing stderr behavior. Output failure is
nonzero; a closed pipe cannot promise a final JSON document or record.

Preserve a successfully validated response before making the next observation,
renewal or cleanup call. On postacceptance failure, emit that last coherent
instance/attempt with a safe typed error, explicitly describing it as last observed
rather than current or final. Extend the existing `InstanceResponse` and
`SceneResponse` with optional presentation `error` fields for this combination;
do not put presentation failures in durable `Instance`/`ExecutionResult` fields.
Use existing `Result`/`Snapshot` and `Error` fields in attached/follow records.
Retain drift and independently observed execution/collection/cleanup results.
Never substitute a later attempt's result for the accepted attempt. No valid
snapshot means an `ErrorResponse`, without invented runtime facts.

A transport failure on a mutation is not proof of rejection, daemon death or
completed cleanup. Distinguish compatibility/preflight refusal from a request
whose acceptance/result could not be observed, using safe operation-specific
context in existing typed errors. Preserve known target IDs and give inspection
or explicit recovery guidance; do not automatically retry mutation or declare
that retry is safe. When acceptance returned no identity, say acceptance is
unknown instead of fabricating an instance. Successful daemon `stop` acknowledges
stopping; it does not establish process exit or lock release.

Do not include raw transport/OS errors, private socket/config contents, resolved
secrets or owner/lease capabilities in the new error context. Preserve explicit
secret inspection, private preparation-token output and declared sensitive startup
disclosure under their existing contracts. A retained last observation must not
re-emit attachment authority or hydrate secret startup values.

### Results, exits and useful human output

Command success answers the command's operation: inspecting a failed execution
can succeed; unhealthy doctor findings return nonzero; an export can succeed for
a failed application, or return an observed result with publication failure.
Preserve `ExportResponse` and its published/path rules. Emit a result-bearing
failure once, without appending another JSON error document.

Keep original terminal exits in their existing fields; absent means unknown,
not zero. An otherwise clean disposable run returns the original application
exit. Runtime, observation, collection or cleanup failure remains nonzero even
with terminal exit zero. SIGINT/SIGTERM on lifecycle-owning waits request bounded
cleanup and retain 130/143 after successful cleanup; cleanup uncertainty remains
visible and nonzero. Sampled status cancellation retains its existing terminal
record and 130/143 without stopping work. Log-follow interruption retains success
and never stops runtime. Export interruption stops waiting/publication observation,
not the run. Foreground serve retains its existing signal shutdown behavior.

Human rendering uses the same observed facts as JSON: instance/attempt and
operation identity, component outcomes and known/unknown terminal exit, failure
and structured detail, collection/cleanup errors, kept resources, drift,
evidence expiry/limitations and appropriate inspect/log/export/cleanup next steps.
Gateway/shared summaries include their actual statuses, warnings and failures.
Do not infer readiness from metadata, claim absence from incomplete cleanup, or
replace actionable detail with styling. Reuse common instance rendering for scene
and attached output instead of duplicating failure lists.

### Verification and canonical guidance

Test Backlot command construction, supported/repeatable flags, semantic rejection
before effects, usage/error hooks and framing, identity/redaction and command exit
interpretation. Exercise the real executable with owned native success/failure,
postacceptance observation/cleanup failure and relevant cancellation paths. Extend
existing focused tests and fixtures; do not test upstream option ordering, equals
syntax or separator token handling. A runtime argument witness verifies Backlot
passed the intended values to the application, not the parser implementation.

Put durable contributor expectations once in the Go practices guide; put user
invocation/framing/exception reference in operations, with links to daemon/evidence
for their existing lifecycle/result details. During this delivery, the owner's
explicit testing directive overrides blanket local `make check`: run narrow,
meaningful checks, with CI as the final gate. A full local suite is reserved for
CI-failure diagnosis or required behavior not covered by CI. This design phase
runs ADR verification only, with no lifecycle or upstream parser tests.

## Consequences

The change reaches CLI registration/actions/rendering, executable error emission,
named presentation response fields and their validation/callers/tests. It adds no
persisted format, migration, writer, endpoint, dependency, config setting,
subscription, retry worker or timer. Existing daemon lifecycle/evidence/config
writers and lease/observation loops remain the authority. New behavior runs only
when invoked; existing background lifecycle/retention work remains unchanged.

Compatibility changes include global JSON, deliberate text exceptions, rejection
of ignored options, optional response error fields and the approved positional-tail
relaxation. Strict JSON consumers must accommodate documented optional fields.
Preflight failure and ambiguous acceptance must not share misleading retry advice.
Last-observed data requires explicit error context to avoid appearing final.

Keep implementation within the issue's estimated scope: 600–1,200 implementation,
700–1,400 test/fixture and 200–400 documentation changed lines. These are planning
ranges, not targets. Flag material growth or a new subsystem/ownership boundary
before expanding; independent presentation and registration work can be reviewed
separately after ADR acceptance without splitting the public contract.

## Alternatives considered

- Keep manual parsers while sharing helpers: preserves grammar but retains a
  second parser and contradicts the owner's explicit library boundary.
- Guarantee JSON for every malformed invocation: requires earlier argv inspection
  or different upstream behavior. Conditional machine output is sufficient.
- Keep existing parsing and only improve docs: smaller, but leaves known ignored
  options, inconsistent error framing and missing identity/actionable results.
- Add one response envelope or error registry: broadens every caller without
  preserving more facts than optional fields on the existing named contracts.
- Return only an error after accepted work: minimizes presentation code but loses
  known identity/results and encourages unsafe retry assumptions.
- Add progress streaming or durable operation recovery: addresses early identity
  delivery/continuation beyond this issue; existing finite and NDJSON modes suffice.

## Revisit when

The pinned framework exposes delimiter provenance; an actual consumer requires
stronger malformed-input framing or cannot tolerate the optional response fields;
or measured usage needs early identity, resumable observation or recovery of
unacknowledged mutation acceptance. Reassess guarantees and scope before adding
parser hooks outside the framework, durable state or background work.

## References

- [Issue 56 and owner decisions](https://github.com/octacian/backlot/issues/56)
- [Go and typed API](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Sampled status](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Attached ownership](20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md)
- [Startup disclosure](20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md)
- [Portable export](20261018T233600000Z-publish-daemon-owned-portable-run-reports.md)
- [Shared API](../architecture.md#shared-api), [Go practices](../go-practices.md)
- [CLI registration](../../internal/cli/command.go), [presentation](../../internal/cli/daemon.go)
- [Client boundary](../../internal/client/client.go), [named API contracts](../../api/v1/)
- [Pinned library parser](https://github.com/urfave/cli/blob/v3.14.0/command_parse.go)
- [Pinned library error hooks](https://github.com/urfave/cli/blob/v3.14.0/command_run.go)
- [Operations](../operations.md), [results and evidence](../evidence.md)
