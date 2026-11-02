---
title: 'Authorize declared sensitive startup details for persistent scenes'
date: '2026-10-22'
status: accepted
implementation: implemented
summary: 'Treat ordered fixture selection as authorization for sensitive persistent startup display while keeping recorded results and observation redacted.'
tags: [security, configuration, lifecycle, cli, api]
supersedes: [/docs/adr/20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md, /docs/adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/53"]
implemented_by: ["https://github.com/octacian/backlot/commit/d498af97777f8196031eab2857ecb43de02a0cda"]
---

# Authorize declared sensitive startup details for persistent scenes

## Context and Problem Statement

Adopter scripts print connection details after startup. Existing fixtures already
resolve values with sensitivity and private capture; a second value system is
unnecessary. Separate reads or retries can otherwise mix attempts or display stale
ports and credentials.

## Considered Options

- Declaration-selected startup details using existing fixture resolution.
- Keep scripts/manual inspection: leaves repeated presentation code.
- A reveal flag: differs from the owner's declaration-based disclosure choice.
- Put secrets in ordinary runtime records: broadens persistence/export leak paths.
- Re-resolve current configuration: can display values the attempt never adopted.

## Decision Outcome

Treat an ordered persistent-scene selection of component fixtures as authorization
to display those selected sensitive values after successful readiness. This is
an explicit exception to prior ordinary-output redaction, not general permission
to hydrate status, plans, durable results, diagnostics or exports. Labels and
identifiers cannot carry credentials; disposable summaries remain outside scope.

Use the accepted ready attempt's retained selection and final resolved values,
including permitted port retries. Pin readiness and sensitive reads to instance
and attempt; never substitute a replacement or current configuration. Keep
sensitive retrieval behind the existing same-user private administration boundary.

Assemble one coherent summary. If any detail is unavailable, omit the entire
summary and return a sanitized warning while preserving successful startup and
known identity/URL. Failed/cancelled startup has no successful summary; unavailable
details never trigger application retry or expose raw retrieval errors.

Only startup presentation changes. Preserve ordinary redacted records, explicit
secret inspection, fixture retention and inherited supervision/recovery/configuration
contracts. Removing a current selection does not revoke a retained attempt's consent.

### Consequences

Startup stdout and saved JSON intentionally contain selected credentials and need
appropriate protection. Application changes can invalidate displayed credentials;
readiness is an observation, not continuing health or authentication proof. Reusing
fixtures adds no secret history/store or background service, and reset/destroy do
not promise erasure from history or backups.

## More Information

[Startup details](/docs/daemon.md#declared-startup-details),
[fixture capture](/internal/daemon/fixtures.go).
