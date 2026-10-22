---
title: 'Retain cumulative daemon state migrations from version 9'
date: '2026-10-22'
status: accepted
summary: 'Retain small transactional Go migration steps from version 9 so users can skip releases without resetting retained state.'
tags: [daemon, state, migration, compatibility]
supersedes: [20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
superseded_by: []
---

# Retain cumulative daemon state migrations from version 9

Retain each real daemon state migration from version 9 onward and apply all
required steps at startup in one existing bbolt write transaction. Users can skip
releases and upgrade with the newest binary, without intermediate binaries or a
state reset. Preserve the existing supported main v6/v8-to-v9 transitions.

This decision replaces only the state-transition policy in the
[native session decision](20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md).
All its supervision, authority, bounded cleanup, confidence, recovery and inherited
configuration/privacy contracts continue in force by reference.

## Context

[Issue #66](https://github.com/octacian/backlot/issues/66) asks for repeatable
compatibility evidence and a maintainable upgrade policy. The approved outcome
sets v9 as the forward baseline. The current opener already validates v6/v8/v9
and fences v6/v8 to v9 in one transaction, but the accepted policy explicitly
excludes a retained chain. Repeating predecessor-specific delivery exercises or
requiring intermediate releases would defeat the approved outcome.

Prefer a tested existing migration library when it fits. The inspected
[golang-migrate interface](https://github.com/golang-migrate/migrate/blob/504568a3cbd23b8754760f55a3d89aec1b0c4963/database/driver.go)
uses reader-based migrations and separate version calls; Goose requires SQL.
[Migrago's Bolt adapter](https://github.com/librun/migrago/blob/a9e990da5b455c35466bcf154192f1ec4756c585/internal/storage/bolt.go)
stores SQL migration history. The bbolt-native
[nmilat manager](https://github.com/ohstr/nmilat/blob/670939430bdd3b3acc86f3bf578f1c2f0d513f7b/relay/migrations/manager.go)
lets steps commit their own batches, then commits the version separately. None
fits caller-owned, all-step rollback without replacing its central behavior.
[Containerd](https://github.com/containerd/containerd/blob/57d315c6ec7c97b407bd62dbefb3b449e3a5d84c/core/metadata/db.go)
demonstrates a small one-transaction step list, but its dispatcher is coupled to
its metadata schema rather than an independently reusable migration library.

## Decision

Keep migration dispatch private to `internal/daemon`, beside the existing opener
and predecessor checks. Use an explicit ordered list of source/destination
versions and Go functions accepting the already-open `*bbolt.Tx`. Permit only
listed forward transitions; reject malformed versions, unsupported predecessors,
missing steps, and newer state. Retain the existing v6/v8 bridge to v9; subsequent
real steps continue from v9. No registry, plugin interface, migration scripting,
second history store, dirty marker, downgrade path or new dependency is needed.
Keep `StateVersion` at 9 until an actual persisted-format change requires a bump;
representative later versions belong only to tests.

The daemon acquires exclusive directory ownership and bbolt access before opening
state. Fresh state initializes directly at the current format. Existing state
uses source-appropriate layout and authority checks before transformation, then
runs the complete path and validates the resulting current authority before
writing the final version fence. Do not apply today's required bucket layout or
future-only decoder requirements to an older format before its step can convert
it. Preserve current v6/v8 validation and incompatible experimental-v6 rejection.
Each future change supplies only the historical decoding needed by that step;
current structs are not historical fixture evidence.

All validation, data operations and the final fence share the existing write
transaction. A step returns errors rather than committing or opening another
transaction. Preserve unknown/passive data and all records outside each step's
explicit transformation. Retain cancellation checks before work and immediately
before commit. Any precommit error or interruption rolls back the whole chain;
commit winning a cancellation race leaves the current format. A later startup
failure never downgrades committed state. Rejected state retains actionable
preservation and matching-binary guidance. Migration performs no provider calls,
process actuation, registry writes or retained-file effects.

Recovery follows successful database open under its existing ownership rules;
opening state never restarts workloads. Operators stop the old daemon normally,
wait for shutdown/lock release, then explicitly start the new binary on the same
directory. Existing v6/v8 shutdown-response compatibility stays bounded as today;
future wire shutdown compatibility must be decided with the relevant real change.
There is no automatic handover, polling restart or promise that older binaries
can read newer state. Preserve incompatible state for diagnosis; reset is not an
ordinary upgrade step.

Capture a nonempty v9 database with the unmodified pinned baseline producer,
record its source revision, binary/build provenance, fixture inputs, commands and
artifact hashes, and commit it with a regeneration procedure. Use only fixture
values and private owned resources. Copy closed database files; tests copy the
fixture to temporary state before opening it and never recover its historical
external paths. Retain existing v6/v8 support/tests without recreating historical
fixture matrices. For each future real format change, capture its predecessor
before changing the producer, retain prior steps/fixtures, and verify opening the
oldest retained fixture through the complete current path and reopening it.
Removing a supported path requires an explicit compatibility decision.

Share transaction failure/interruption checks rather than repeating runtime
lifecycle suites per version. Test an ordered chain with real bbolt bucket/value
changes and synthetic test-only versions, including rollback of an earlier step
when a later step fails. Retain current rejection, authority, cancellation and
postcommit-startup coverage. Add genuine v9 fixture open/reopen and rejected-copy
preservation coverage early; fixture evidence and synthetic algorithm tests serve
different purposes. Document the next-change procedure in contributor guidance.

## Consequences

Users can skip releases while retaining state and ownership semantics. Production
scope is one startup dispatcher using an existing database transaction; ordinary
writers, public APIs and background owners keep their current boundaries. Tests
and fixture provenance carry most of this issue's work. A native-only capture
provides real records without requiring Docker, Caddy or trust changes; it does
not establish historical provider coverage.

Retaining steps and fixtures is an ongoing compatibility obligation. A large
future rewrite can increase startup latency and bbolt transaction memory; it
requires a fresh scope assessment rather than silent batching that weakens
all-step rollback. No current transformation or schema bump is justified.

## Alternatives considered

- **Keep only the latest predecessor or require intermediate binaries.** Less
  retained code, but fails the approved skip-release outcome.
- **Adopt an inspected migration package.** Preferable if it removes maintained
  code while preserving the transaction boundary; these candidates require a
  custom driver, transaction redesign or unrelated storage integration.
- **Minimal local Go steps.** Chosen: retains the existing proven transaction,
  avoids a migration engine, and expresses only real Backlot transitions.
- **Commit each step and resume after failure.** Bounds transaction size, but
  exposes partially upgraded state and breaks the approved rollback guarantee.
- **Recreate all old databases with current structs.** Cheap unit inputs, but
  cannot establish compatibility with the actual historical producer.

## Revisit when

A suitable maintained bbolt library accepts a caller-owned transaction with less
integration code; real migrations outgrow the simple dispatcher or transaction
budget; removal of a retained compatibility path is proposed; or a real format
change needs external-file effects or different shutdown wire compatibility.

## References

- [Issue #66](https://github.com/octacian/backlot/issues/66)
- [Current state opener](../../internal/daemon/state.go), [authority preflight](../../internal/daemon/state_authority.go), and [legacy validation](../../internal/daemon/state_upgrade.go)
- [Upgrade tests](../../internal/daemon/state_upgrade_test.go), [interruption tests](../../internal/daemon/state_integration_test.go), and [opt-in actual predecessor evidence](../../internal/daemon/state_actual_predecessor_test.go)
- [State and recovery architecture](../architecture.md#state-and-recovery)
- [State operation](../containers.md#state-compatibility) and [testing guidance](../testing.md)
