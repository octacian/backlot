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
startup summary. Selection authorizes automatic display of selected sensitive
values in human and typed JSON startup output. Reuse fixture resolution and dedicated
sensitive retrieval; keep plans, durable results, observation, diagnostics and
exports redacted.

This replaces only the ordinary-output redaction promise in the
[offline planning decision](20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md)
and the inherited run-redaction promise in the
[native-session decision](20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md).
All other requirements and inherited contracts remain unchanged, including native
supervision, cleanup, recovery, state compatibility and private configuration.

## Context

[Issue 53](https://github.com/octacian/backlot/issues/53) identifies adopter scripts
that print connection details after startup. Fixtures already resolve literal,
configured-input and generated-resource values with sensitivity propagation and
private capture, so no separate value system is needed.

The owner chose declaration-based disclosure instead of the issue's proposed
reveal flag. The same-user private administration socket remains the access
boundary.

Before this change, separate execution/private-snapshot reads could mix attempts
during restart/reset, and readiness polling could observe a replacement attempt.
Fixture capture also preceded bind-conflict retries, leaving abandoned ports in
captured values. Summaries therefore need coherent attempt reads and final values.

## Decision

Add an ordered scene `startup_details` list selecting a component fixture and a
nonempty display label. Validate selected-component membership and fixture
existence before runtime effects; reject nonempty selections on disposable scenes.
Retain selection metadata in the redacted plan and snapshot. Values and sensitivity
come from existing fixtures; selections add no expressions, scripts or graph edges.
Credentials must not appear in labels or identifiers.

After successful persistent readiness, `run`, `scene start`, `restart` and `reset`
show the selected details, including already-running starts. Use the accepted
attempt's retained selections and values despite current configuration drift.
Stop/run retains its snapshot; restart/reset adopt selections through existing
compatibility rules, preserving pre-stop drift rejection and generated-secret
retention with storage. Pin readiness polling to the accepted attempt: replacement
before observed readiness is a conflict, never a successful replacement summary.
Failed or cancelled startup emits no successful summary.

Refresh selected fixture captures after permitted port retries and before
persistent readiness. Replace selected records and private resolved values without
duplicates, preserving unselected captures and failed/disposable behavior. Selected
resource/service port fixtures, sensitive or otherwise, must match the final
listener. Failed refresh makes a detail unavailable rather than retaining a stale
value or failing otherwise successful startup.

Keep `Instance`, `ExecutionResult` and persisted fixture records redacted. Attach
named shared summary entries, attempt identity and sanitized warnings only to the
finite startup presentation result, nested in the existing scene envelope where
applicable. Emit one JSON document; human output follows instance/URL information.
Generic status formatting must not hydrate sensitive values.

Read nonsensitive values from the ready response's retained fixture records.
Retrieve selected sensitive values through `/v1/fixtures/secret`, extending its
request with an optional expected attempt and its response with verifiable
instance/attempt identity. Pinned reads require a persistent ready attempt and
coherent metadata/private values in one bbolt read transaction; reject missing or
mismatched data. Validate response identity, component/name, sensitivity and
cardinality in the client. Preserve unpinned explicit secret inspection, including
after teardown. No new endpoint or authorization capability is needed.

Assemble against one observed ready attempt, without mixing replacement values or
rereading current inputs. If any detail is unavailable, omit the entire summary
and return a sanitized warning while preserving startup identity, URL and success.
Do not expose raw retrieval errors or retry/restart the application. Human warnings
use stderr; JSON carries typed warning data. Retrieval uses the existing request
timeout and caller cancellation, without background retries. The summary records
observed readiness, not continuing health or credential validity.

Explicit secret inspection and application-owned logs/artifacts retain their
existing contracts. Fixture retention is unchanged; reset/destroy do not promise
secure erasure from history or backups.

## Consequences

Existing snapshot writers and the private fixture store suffice; no new storage,
migration, dependency or background worker is needed. Historical records without
selections have no summary. With no selections, no refresh or sensitive retrieval
runs. No machine configuration, trust/DNS setup or service installation is added.

Startup stdout and saved JSON now intentionally contain selected credentials and
must be protected accordingly. Removing a current declaration does not revoke a
retained attempt's selection; restart/reset adoption rules still apply.
Applications own seeding, authentication and fresh-only/each-start policy choices;
application-side changes can invalidate the displayed credentials.

## Alternatives considered

Keep scripts or require manual fixture inspection: leaves the presentation helper
this feature removes. A reveal flag conflicts with the owner's disclosure choice;
revealing all fixtures exceeds the declared selection.

Return sensitive values in ordinary runtime records: broadens persistence and
export leak paths. A dedicated summary endpoint could return all values atomically,
but existing sensitive retrieval with attempt validation and all-or-unavailable
assembly suffices. Unavailability is acceptable without per-attempt secret history,
caches or additional lifecycle locks.

Re-resolve current configuration: can print unadopted or newly generated values.
Log scraping, display scripts and Backlot-owned account provisioning introduce a
separate value or credential system outside this feature.

## Revisit when

An adopter needs disposable summaries, independent fixture history, stronger
continuation across replacement, or an access boundary beyond the same-user
socket. Reconsider scope before adding persistent summary storage or new services.

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
