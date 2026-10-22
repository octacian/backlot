---
title: 'Authorize declared sensitive startup details for persistent scenes'
date: '2026-10-22'
status: accepted
summary: 'Treat ordered fixture selection as authorization for sensitive persistent startup display while keeping recorded results and observation redacted.'
tags: [security, configuration, lifecycle, cli, api]
supersedes: [20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md, 20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
superseded_by: []
---

# Authorize declared sensitive startup details for persistent scenes

A persistent scene may select existing component fixtures for its successful
startup summary. Selecting a sensitive fixture authorizes automatic display of
that selected value in human and typed JSON startup output. Reuse fixture
resolution and dedicated sensitive retrieval; keep durable results, plans,
ordinary observation, diagnostics and exports redacted.

This decision replaces the ordinary-output redaction promise in the
[offline planning decision](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
and the inherited run-redaction promise in the
[native-session decision](20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md).
All other requirements of both decisions and their inherited contracts are
incorporated unchanged by reference, including native supervision, cleanup
confidence, recovery, the state-v9 transition, offline plan redaction and private
configuration inspection authority. The older
[private configuration record](20261021T001732173Z-register-private-project-configuration.md)
retains its existing lineage links and rationale. Accepted after independent
architecture review and fix verification with coordinator approval on 2026-10-22;
implementation and runtime acceptance remain separate obligations.

## Context

[Issue 53](https://github.com/octacian/backlot/issues/53) identifies adopter
scripts that print connection details after startup. Fixtures already resolve
literal values, configured inputs and generated resources with sensitivity
propagation, and capture actual resolved values privately. Reusing that system
avoids a second value language or application-output scraping.

The issue originally requested an invocation reveal flag. The owner's approved
delivery contract instead selects automatic display, including sensitive values,
for persistent run, scene start, restart and reset, including repeated starts.
The declaration is the display authorization; no reveal flag is required.
This deliberately changes the above accepted promises, rather than classifying
startup credentials as ordinary redacted output. The existing same-user private
administration socket remains the security boundary.

Current fixture capture writes sensitive resolved literals to the private
`secrets` bucket and only redacted records to `ExecutionResult`. The fixture
endpoint reads the execution and private snapshot separately; requests identify
an instance but not an attempt. A reset or restart between reads can pair old
metadata with new values. Current CLI readiness loops can also observe a newer
attempt on the same ID. Neither behavior establishes summary consistency.

Capture also precedes component launch and permitted bind-conflict port retries.
A ready attempt can therefore retain a fixture containing an abandoned port.
Attempt fencing alone cannot establish actual connection values within an attempt.

## Decision

Add an ordered scene `startup_details` list of component, fixture and label
selections. Each selection names an existing fixture on a component selected in
that scene and has a nonempty display label. Validate declarations before runtime
effects and preserve declaration order. Reject nonempty selections on disposable
scenes. The list supplies no new values, expressions, scripts or implicit graph
edges. Carry only this selection metadata in the redacted plan and retained
snapshot; credentials belong in fixture values, never labels or identifiers.

After successful persistent readiness, `run`, `scene start`, `restart` and `reset`
render the selected fixtures automatically. Already-running starts do the same.
Use selections and actual resolved fixture values from the accepted retained
attempt, including when current declarations/configuration drift. Stop/run keeps
its existing snapshot semantics; restart/reset adopt selections only through
existing snapshot acceptance and retained-data compatibility rules. Do not weaken
pre-stop drift rejection or change generated-secret retention with storage.
A failed, cancelled or replaced startup attempt cannot emit a successful summary.
Pin readiness polling to the accepted execution attempt; replacement before
observed readiness is a conflict, not readiness of the original startup.

After permitted port retries, refresh selected fixture captures before successful
persistent readiness using the existing resolver and private store. Replace the
selected records and their sensitive resolved literals rather than append duplicate
records; preserve unselected captures and existing failed/disposable capture
behavior. A selected resource/service port fixture, including a sensitive fixture,
must reflect the final launched listener. If refresh cannot resolve a selected
detail, make that detail unavailable rather than reuse the early capture or fail
otherwise successful startup. Add one focused forced-port-retry proof covering
ordinary and sensitive selected ports.

Keep `Instance`, `ExecutionResult` and persisted fixture records redacted. Add
named shared types for transient startup summary entries, attempt identity and
sanitized unavailability warnings, attached only by the startup CLI presentation
path to its finite typed result. Scene startup nests that result in its existing
scene envelope. Emit one JSON document, with no appended human text; a human
summary prints labels and values after existing identity/URL output. Do not
hydrate shared status objects or teach generic status formatting to reveal values.

Use the ready response's retained selection metadata and ordinary fixture records
for nonsensitive entries. Retrieve selected sensitive entries through existing
`/v1/fixtures/secret` authority. Extend its named request with an optional expected
attempt and its response with verifiable instance/attempt identity for pinned
reads. A pinned retrieval must read fixture metadata and private resolved value
coherently, require the expected attempt to be persistent and ready, and reject
missing/mismatched values or attempts. A single bbolt read transaction over the
existing records is sufficient: replacement and capture occur before a new
attempt becomes ready. Preserve existing unpinned explicit secret inspection,
including after teardown. Validate response identity, selected component/name,
sensitivity and cardinality in the client before displaying any returned value.
No new summary endpoint or persistent authorization capability is needed.

Assemble a summary against one observed ready attempt; never merge values from
replacement attempts or reread current project inputs to fill gaps. If any
selected value is unavailable or a pinned retrieval fails, omit the incomplete
summary and return a sanitized startup-details-unavailable warning with the
successful startup identity/URL and exit status intact. Do not turn this
presentation failure into application failure, retry/restart the application, or
copy raw transport/server error text into the warning. Human warnings use stderr;
JSON carries typed warning data. Retrieval is bounded by the existing client
request timeout and caller cancellation, with no background retries. A later
stop can invalidate current readiness after observation; this summary attests
the observed attempt, not continuing health or immutable application credentials.

Ordinary status/follow, inspect, plan, doctor, fixture discovery, results and
portable export never contain these hydrated values. Existing explicit `secret`
inspection and application-owned logs/artifacts retain their existing contracts.
There is no change to fixture retention/erasure: private snapshots and fixture
values remain durable independently of log/artifact pruning; reset/destroy do
not promise secure erasure from history or backups.

Extend the existing macOS quickstart with an application-owned seed job and
login check using identical declared email/password sources for seed environment
and fixtures. Use existing input/resource/environment/dependency mechanisms.
Show that fresh-only jobs initialize retained state and each-start jobs rerun;
the application chooses its policy and owns account creation/authentication.
Do not create a new example or a product provisioning subsystem.

## Consequences

The existing daemon lifecycle and private fixture store suffice. Persistence
adds optional selection metadata through existing preparation/restart/reset
snapshot writers, not new secret storage, tables, migrations, timers, workers or
dependencies. Historical records without selections have no summary. Private
fixture capture gains a bounded selected-value refresh before persistent readiness,
fixture retrieval gains an attempt-coherent read; startup CLI callers gain finite
selected-value retrieval and transient output assembly. When no details are
selected, no additional secret requests or background work run.

Configured startup output now intentionally contains credentials, including JSON
that callers may save. Operators/project authors must review selections and
protect startup output as they already protect explicit secret output. Removing
a selection from current files does not revoke a retained attempt's selection;
existing restart/reset adoption rules determine when it changes. No new machine
configuration, trust/DNS setup, service installation or monitoring task is needed.
Application seeding remains its author's work, and displayed credentials may
later be invalidated inside the application.

Acceptance requires sensitivity and negative leak coverage, retained/drift and
attempt-race coverage, all startup paths, one-document JSON, failed readiness,
nonfatal retrieval failure and real seeded-application equality. Proposal checks
are distinct from implementation, provider and published-revision evidence.

## Alternatives considered

Keep scripts or require manual fixture inspection: usable today but leaves the
presentation helper the owner requested removing. A reveal flag preserves the
original issue's redaction default, but contradicts the explicitly approved
automatic-display contract. Reveal all fixtures exposes more than the declared
selection and is unnecessary.

Return sensitive fixtures in ordinary run/status/execution records: fewer client
calls, but broadens serialization, persistence and export leak paths. A dedicated
summary endpoint could atomically return every selected detail, but the existing
sensitive endpoint plus attempt validation and all-or-unavailable assembly meets
the requirement without a second retrieval authority. Per-attempt secret history,
summary caches or new lifecycle locks are unnecessary when replacement can simply
make retrieval unavailable. Revisit only on evidence that this bounded failure
is unacceptable.

Re-resolve current configuration after startup is smaller than retained retrieval,
but can print unadopted credentials or newly generated values. Scraping seed logs,
adding display scripts or provisioning accounts in Backlot introduces an unrelated
value/credential system and is outside the approved scope.

## Revisit when

An adopter needs summaries on disposable results, history independent of retained
fixture state, stronger continuation across concurrent replacement, or an access
boundary stronger than the same-user socket. Reopen authorization and lifecycle
scope before introducing persistent summary storage, new workers or endpoints.

## References

- [Issue 53](https://github.com/octacian/backlot/issues/53)
- [Fixture capture and retrieval](../../internal/daemon/fixtures.go)
- [Private snapshot reads and replacement](../../internal/daemon/runtime_state.go)
- [Fixture resolution and private planning values](../../internal/plan/plan.go)
- [Lifecycle and mutation ownership](../../internal/daemon/runtime.go)
- [Run/restart/reset presentation](../../internal/cli/runtime.go)
- [Scene startup presentation](../../internal/cli/scene.go)
- [Fixture client validation](../../internal/client/runtime.go)
- [Results and evidence contracts](../evidence.md)
- [Portable export decision](20261018T233600000Z-publish-daemon-owned-portable-run-reports.md)
- [Recorded redacted scene status](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Existing macOS quickstart](../../examples/macos-quickstart/README.md)
