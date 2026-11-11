---
title: 'Use framework parsing and preserve truthful CLI results'
date: '2026-10-27'
status: accepted
implementation: implemented
summary: 'Use urfave built-in parsing and existing named result contracts to make CLI invocation and failure interpretation predictable without another parser or response framework.'
tags: [cli, contracts, diagnostics, compatibility]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/56"]
implemented_by: ["https://github.com/octacian/backlot/commit/74a073d52dca118ba02833a834a91ec72db5d493"]
---

# Use framework parsing and preserve truthful CLI results

## Context and Problem Statement

Manual parsing duplicated library behavior and produced inconsistent invocations,
ignored options and machine output mixed with help. Later observation failures
could also discard accepted identity or independently known application outcomes,
encouraging unsafe retries.

## Considered Options

- Use urfave grammar and preserve existing named results with truthful errors.
- Consolidate custom parsers: retains duplicated grammar and delimiter handling.
- Guarantee JSON for every malformed invocation: needs early argument inspection.
- Return only errors after acceptance: loses known identity and outcomes.
- A universal response/recovery framework: expands compatibility and lifecycle scope.

## Decision Outcome

Use urfave's built-in parser, inherited global JSON and usage/error hooks. Backlot
owns command semantics and safe diagnostics, without scanning/reordering raw
arguments or reconstructing separator positions. Scene-command tails follow library
grammar; the maintained CLI reference owns exact syntax and supported options.

After JSON is recognized, usage failures produce structured output without automatic
human help on stdout. Earlier parser failures may use stderr/nonzero status;
malformed-input JSON is intentionally conditional rather than implemented with a
second pre-parser.

Preserve named finite results and NDJSON modes. After accepted work, retain the last
validated matching instance/attempt, drift and independent outcomes with an explicit
last-observed error. Presentation errors are neither persisted nor valid daemon
wire snapshots. Exclude private capabilities and hydrated startup secrets from
retained failure output.

Keep application exits independent of orchestration, collection and cleanup errors.
A missing mutation reply means acceptance/completion may be unknown, never proof
of rejection, completed cleanup or permission for automatic retry.

### Consequences

CLI invocation becomes consistent with the chosen library, while compatibility
changes include global JSON, accepted unseparated tails, stricter unsupported-option
errors and optional presentation errors. Consumers must distinguish observations
from final/current state. Existing daemon lifecycle authority remains unchanged;
stronger framing or resumability needs a demonstrated consumer and new decision.

## More Information

[CLI contract](/docs/cli.md#cli-invocation-and-results),
[adapter rules](/docs/go-practices.md#cli-adapters).
