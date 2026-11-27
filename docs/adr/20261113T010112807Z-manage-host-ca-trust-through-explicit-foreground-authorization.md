---
title: "Manage host CA trust through explicit foreground authorization"
date: 2026-11-13
status: accepted
implementation: implemented
summary: "Manage host trust through explicit foreground stock tools, provisioning receipts, best-effort consumer warnings and bounded platform trust observations."
tags: [gateway, https, trust, authorization, persistence, platforms]
supersedes: []
superseded_by: []
related: [/docs/adr/20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md, /docs/adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md, /docs/adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md, /docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261106T230242771Z-recover-device-only-bindings-after-verified-consumer-settlement.md, /docs/adr/20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md]
tracking: ["https://github.com/octacian/backlot/issues/95"]
implemented_by: ["https://github.com/octacian/backlot/pull/148"]
---

# Manage host CA trust through explicit foreground authorization

## Context and Problem Statement

Managed HTTPS needs explicit host trust installation and retirement without
concealing CA drift. Replace the inherited host-trust prohibition in the related
history decision with foreground operator authority; its lifecycle and erasure
rules remain in force. Preserve root pinning, private CA ownership,
application-selected trust and external gateway ownership.

## Considered Options

- Manual stock commands plus hints and a daemon-cache fix: smallest surface, but
  leaves operators to assemble provenance, exact targeting and recovery.
- Wrap [Caddy trust/untrust](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddypki/command.go):
  also targets Firefox/Java, fetches roots from a mutable endpoint and uses
  name/serial-derived Linux removal paths; insufficient exact-effect control.
- Foreground stock OS tools, provisioning receipts and fresh verification: chosen
  for bounded ownership and best-effort consumer warnings.
- Mandatory gateway enrollment or a privileged broker: stronger coordination costs
  more than the accepted best-effort consumer protection requires.

## Decision Outcome

Expose gateway trust status/install/retire through the CLI and named shared
result contracts. The unprivileged daemon supplies a read-only public-root
snapshot validated against its managed identity, protected CA state and pinned
DER SHA-256; keys and private
capabilities stay private. Report root-specific trusted/untrusted/unknown
observations separately from certificate presence, tool success and readiness.
Observation failures mean unknown.

Gateway start reports the fingerprint, bounded host observation and exact state-scoped
trust command without depending on receipt health or mutation locks. Installation
requires explicit foreground authorization, never a startup offer. Missing host
trust does not fail gateway start; integrity and published-scene TLS checks remain.

### Authority and consumer impact

Retirement requires verifiable Backlot-managed CA provenance **and exact
Backlot-provisioned trust effects**. Receipts bind validated public certificate
bytes, OS target and intent/outcome, distinguishing new effects from pre-existing
entries/settings. Preserve external effects even for an identical root. Names,
arbitrary paths and acknowledgement confer no ownership. Permissioned receipts
use the trusted-local-operator boundary, not attestation against that operator.

Revalidate the pin, snapshot and target before effects. Retained public evidence
can authorize retirement after the source daemon, keys or scope are gone, or the
certificate expires. Ambiguous provenance or indistinguishable external effects
require manual handling.

Known sources support best-effort warnings, not complete discovery or enrollment.
Acknowledging impact permits breakage of unobserved scopes/applications, never
waives ownership and adds no cross-daemon start lock or requirement-release
lifecycle. Replacement first proves the selected new root's OS trust under the
platform rules below; standalone retirement accepts loss of dependent HTTPS trust.

### Effects, receipts and recovery

Use foreground stock tools with normal OS authorization: exact certificate bytes
and explicit keychain/trust domain with macOS `security`; a fingerprint-addressed
CA source and `update-ca-certificates` on Ubuntu/Debian. Preserve conflicts. Linux
collection refresh and configured distribution hooks can affect more than one
root. Browser automation, DNS changes, CA regeneration and privileged services
remain outside this decision. [Infrastructure controls](/docs/adr/20261112T235351793Z-compose-infrastructure-controls-with-verified-shutdown-completion.md)
do not install host trust.

macOS Security may require interactive administrator authentication even under
sudo. Warn before retirement and retain stock foreground authorization. Accept
retirement as a manual acceptance boundary with exact-root recovery and preserved
receipts after interruption; unattended headless completion is unproven. A hosted
wait does not establish its cause, withdrawal or descendant termination on
cancellation. Ubuntu retains automated owned-host mutation coverage.

Only foreground commands write permissioned, atomic, versioned public receipts
in one documented account-local location outside daemon erasure, serializing
mutations and updates with the existing
file-lock library. Journal intent before effects and observations afterward;
retain authority, exact targets and useful warning/recovery observations. No
daemon receipt writer, bbolt migration or background registry is needed.

Verify postconditions: stock success alone is insufficient. Report interrupted
or denied operations as partial/unknown, separating trust withdrawal, entry
deletion and residual trust. Cooperative locks and exact pre/post checks cannot
exclude unrelated administrators; use a controlled maintenance window. Drift
stops further destructive work but cannot undo an external race.

Receipt loss, corruption, unsupported versions or known restoration must not
block status or gateway start. Preserve damaged evidence; restore compatible
tracking or explicitly start clean tracking for future validated effects.
Unsupported writers preserve receipts unchanged. Restoration can leave stale
authority; neither clean tracking nor reconciliation invents old removal rights.
Legacy/manual or uncertain effects require manual OS recovery.

### Fresh verification

Installation/replacement proof is root-specific in the selected OS domain and
excludes certificate-file overrides. macOS accepts configured OS trust: exact
supported admin TLS root settings, exact System public entry and current CA
dates. Native Go SSL evaluation cannot reliably prove standalone CA acceptance;
requiring it would block valid installation. Configured trust does not establish
source-account acceptance or override user deny/other stores. Ubuntu/Debian use
fresh exact-root Go verification. File presence, supplied-root pools,
override-only acceptance or TLS through another root are insufficient.

Report daemon-effective standalone root acceptance separately under its retained
environment and Go verifier/file-override policy. Unsupported native proof stays
unknown. Browser/application trust and normal server-leaf readiness remain
separate; this narrower macOS guarantee avoids a new leaf-acquisition, provider,
private-key or root-loader contract.

Use bounded, joined, unprivileged invocations of the same executable for fresh Go
observations, publication readiness, doctor system TLS and daemon HTTPS probes.
Preserve hostname/date/chain checks, owned destinations, publication markers,
redirects, cancellation and readiness semantics; label explicit-root diagnostics
separately. A new transport or clone of [Go's cached SystemCertPool](https://pkg.go.dev/crypto/x509#SystemCertPool)
cannot refresh Linux roots; macOS normally verifies live, but Go 1.27 file
overrides can also cache roots. Fresh processes reuse Go policy without another
root loader or TLS bypass.

### Consequences

Receipts add durable ownership/recovery metadata and stock-platform adapters,
without a new library dependency or work while unused. Best-effort warnings can
miss consumers and lost authority can require manual cleanup, avoiding a second
coordinated state system for ordinary gateway use.

Targets remain macOS ARM64, Ubuntu 24.04 amd64 and Debian 13.4 amd64. Stock-tool
source/help establishes plausibility; owned platform evidence must establish
actual effects, recovery and fresh verification before delivery claims.
