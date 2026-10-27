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

Delegate public command parsing to the pinned urfave/cli library and preserve
existing named result contracts. This removes a second parser while making
failures actionable without losing accepted execution identity or independently
observed outcomes. Existing lifecycle authority and disclosure rules still apply.

## Context

[Issue 56](https://github.com/octacian/backlot/issues/56) identified inconsistent
invocation syntax, ignored options and machine output mixed with human help.
Manual parsing duplicated library behavior, while later observation failures
could discard an accepted instance or attempt. A generic error alone also loses
useful results, such as the original application exit or completed evidence.

The owner chose native library grammar and conditional machine output for
malformed invocations over custom parsing to preserve stricter guarantees.

## Decision

Use urfave's built-in parser for every public command, including global `--json`
and framework usage/error hooks. Backlot owns command construction, semantic
validation and safe diagnostics. It does not scan or reorder raw arguments, recover
delimiter positions, or duplicate the library's parser guarantees in tests.

For `run` and `plan`, the first positional selects the scene and the remaining
positionals form the terminal-job arguments. Accept tails with or without `--`,
which protects flag-like application arguments. The library consumes the separator
without exposing its position; requiring it for every tail would need custom
parsing. Reject unsupported options and tails for operations that cannot use them.

When JSON mode has already been recognized, usage failures produce a structured
error without automatic human help on stdout. Earlier parser failures may use
stderr and a nonzero exit. Do not add an argument pre-scan to guarantee JSON for
every malformed invocation. Explicit help and service/text exceptions remain
part of the [CLI reference](../operations.md#cli-invocation-and-results).

Preserve existing named finite JSON results and NDJSON modes rather than adding a
universal envelope. On postacceptance observation or cleanup failure, retain the
last validated matching instance/attempt, drift and independent execution outcomes,
labelled as last observed with a safe error. Add optional presentation errors to
`InstanceResponse` and `SceneResponse`; reuse existing attached/follow error fields.
These errors are not persisted or accepted as daemon wire snapshots. Retained
failure output excludes private capabilities and hydrated startup secrets.

Keep original application exits separate from orchestration, collection and
cleanup failures. Human output uses the same observed facts as machine output.
A missing mutation reply means acceptance or completion may be unknown; it does
not establish rejection, completed cleanup or permission to retry automatically.

Command syntax, framing, interruption behavior and recovery guidance belong in the
[operations guide](../operations.md#cli-invocation-and-results), [daemon guide](../daemon.md)
and [evidence guide](../evidence.md). Contributor implementation and testing rules
belong in [Go practices](../go-practices.md#cli-adapters).

## Consequences

The change spans CLI adapters, shared presentation types and client validation,
without adding persistence, endpoints, dependencies or background work.
Existing daemon owners remain responsible for lifecycle and cleanup.

Compatibility changes include global JSON, acceptance of unseparated terminal
tails, rejection of formerly ignored options and optional result error fields.
Strict consumers must accommodate those fields and distinguish a last observation
from current or final state. Malformed-input JSON remains conditional.

## Alternatives considered

- Keep or consolidate manual parsers: preserves stricter grammar but retains
  duplicated parsing and conflicts with the chosen library boundary.
- Guarantee JSON for every malformed invocation: requires earlier argument
  inspection or upstream support; conditional machine output is sufficient.
- Return only errors after accepted work: simpler output, but loses known identity
  and results and can encourage unsafe retries.
- Introduce a universal envelope, progress stream or durable recovery mechanism:
  expands compatibility and lifecycle obligations beyond the demonstrated need.

## Revisit when

An actual consumer needs stronger malformed-input framing, delimiter provenance,
early identity delivery or resumable observation. Reassess the required guarantee
and library support before adding parsing or lifecycle machinery.

## References

- [Issue 56 and owner decisions](https://github.com/octacian/backlot/issues/56)
- [Go and shared typed API](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Sampled observation](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Attached ownership](20261022T194804759Z-bind-attached-persistent-execution-to-one-daemon-owned-attempt.md)
