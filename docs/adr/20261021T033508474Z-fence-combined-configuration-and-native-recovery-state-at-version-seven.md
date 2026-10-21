---
title: 'Fence combined configuration and native recovery state at version seven'
date: '2026-10-21'
status: accepted
summary: 'Use state version 7 without migration to distinguish combined private configuration and native ownership recovery from either version-6 implementation.'
tags: [configuration, native, recovery, persistence, compatibility]
supersedes: [20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md, 20261021T001732173Z-compose-machine-configuration-with-registered-private-project-inputs.md]
superseded_by: []
---

# Fence combined configuration and native recovery state at version seven

Use daemon state version 7 for the combined registered private configuration and
native ownership recovery implementation. Reject v5, v6 and other unsupported
state unchanged, without migration. Use the matching older binary to clean up
owned resources and stop its gateway/daemon before choosing a fresh state directory.
The project registry remains independent of daemon state.

## Context

The configuration PR and native recovery on `main` independently chose version 6.
A shared version cannot distinguish a daemon that understands private-source and
effective-configuration provenance from one that understands native ownership
waivers alone. The latter could accept configuration-sensitive requests without
interpreting the registered layer. Native recovery also introduced a narrow v5
transition, while project composition deliberately rejects older snapshots rather
than inventing missing private provenance. The owner approved keeping both
features and advancing state with rejection of v5/v6, without migration.

## Decision

Retain all configuration association, composition, setup, validation, provenance,
retention and operator responsibilities from the superseded configuration decision.
Retain all boot provenance, authenticated authority, explicit waiver, collection,
warning, deletion and saved gateway configuration guarantees from the superseded
native recovery decision, including its referenced supervision/HTTPS chain.
Only their state-version and transition provisions change here.

Advance the shared `StateVersion` to 7. Require exact compatibility on daemon open
and before configuration-sensitive client mutations, including when an older daemon
is already running. Reject unsupported state before recovery or retained-resource
mutation and preserve database bytes and retained files. Do not apply the earlier
v5-to-v6 additive transition or retrofit old snapshots. Keep native ownership
warnings and effective configuration digests together in exported reports.

## Consequences

Both features remain available under one compatibility fence, and version-6
binaries cannot interpret the combined private state. Existing v5/v6 users must
perform cleanup using their matching binary and select fresh daemon state; this
replaces the earlier native recovery decision's automatic v5 transition. A new
state directory does not clean up old resources. Preserve old state until cleanup
is verified. Existing independent project setup remains reusable.

## Alternatives considered

Reusing version 6 would admit daemons that lack one feature's semantics. Advancing
to 7 while migrating older snapshots would require a separate provenance policy
and migration implementation; the owner selected rejection instead. Keeping only
one feature would discard independently intended behavior.

## Revisit when

A supported migration with explicit historical provenance semantics is required,
or another incompatible persisted contract requires a new state version.

## References

- [Registered configuration decision](20261021T001732173Z-compose-machine-configuration-with-registered-private-project-inputs.md)
- [Native ownership recovery decision](20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md)
- [State compatibility](../containers.md#state-compatibility)
- [Shared version](../../api/v1/daemon.go)
- [State opening](../../internal/daemon/state.go)
- [Client compatibility](../../internal/client/configuration_test.go)
