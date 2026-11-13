---
title: "Manage host CA trust through explicit foreground authorization"
date: 2026-11-13
status: accepted
implementation: in-progress
summary: "Manage host trust through explicit foreground stock tools, provisioning receipts, best-effort consumer warnings and fresh TLS verification."
tags: [gateway, https, trust, authorization, persistence, platforms]
supersedes: []
superseded_by: []
related: [/docs/adr/20261014T033700000Z-support-managed-local-ca-and-external-caddy-gateways.md, /docs/adr/20261014T215536946Z-extend-managed-https-to-explicit-container-consumers.md, /docs/adr/20261016T201500000Z-support-explicit-linux-managed-https-reachability.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md, /docs/adr/20261028T234801079Z-retain-bounded-attempt-history-and-erase-it-on-successful-destroy.md, /docs/adr/20261106T230242771Z-recover-device-only-bindings-after-verified-consumer-settlement.md, /docs/adr/20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md]
tracking: ["https://github.com/octacian/backlot/issues/95"]
implemented_by: []
---

# Manage host CA trust through explicit foreground authorization

## Context and Problem Statement

Managed HTTPS needs explicit host trust installation and retirement.
[Issue 94](https://github.com/octacian/backlot/issues/94#issuecomment-6839378023)
closed without reproducing root replacement; repeated installation must not conceal
CA drift. Replace the earlier prohibition on Backlot-driven host trust effects
with foreground operator authority. This narrowly amends the inherited host-trust
prohibition in the related current history decision; its unrelated lifecycle and
erasure decisions remain in force. Preserve root pinning, private CA ownership,
application-selected trust and external gateway ownership.

## Considered Options

- Manual stock commands plus better hints and a daemon-cache fix: smallest surface,
  but leaves operators to assemble provenance, exact targeting and recovery.
- Wrap [Caddy trust/untrust](https://github.com/caddyserver/caddy/blob/v2.11.7/modules/caddypki/command.go):
  also targets Firefox/Java, fetches installation roots from a mutable endpoint and
  uses name/serial-derived Linux removal paths; insufficient exact-effect control.
- Foreground stock OS tools, provisioning receipts and fresh verification: chosen
  to provide the workflow with bounded ownership and best-effort consumer warnings.
- Mandatory gateway enrollment or a privileged broker: stronger coordination costs
  more than the accepted best-effort consumer protection requires.

## Decision Outcome

Expose gateway trust status/install/retire through the CLI and named shared result
contracts. The daemon supplies a read-only public-root snapshot validated against
its managed identity, protected CA state and pinned DER SHA-256. Keep keys and
private scope capabilities private. Results identify fingerprint, state/source,
store/domain, provenance and observation context. Distinguish trusted, untrusted
and unknown from gateway readiness, certificate presence and command success;
observation failures mean unknown.

Gateway start reports fingerprint and host trust status, with the exact Backlot
trust command for the selected state/root and necessary follow-up when untrusted
or unknown. This bounded observation is independent of receipt health and trust
mutation locks. Installation remains an explicit foreground action, without an
interactive startup offer. Missing host trust does not fail an otherwise valid
gateway start; existing integrity checks and published-scene verified TLS remain.

### Authority and consumer impact

Retirement requires both verifiable Backlot-managed CA provenance and evidence
that Backlot provisioned the exact trust effect. At installation, retain the
validated daemon snapshot and bind intent/outcome to its public certificate bytes
and OS target, distinguishing pre-existing entries/settings from newly created
effects. Preserve external effects even for an identical root. Certificate names,
arbitrary paths and user acknowledgement confer no ownership. Permissioned receipts
use the existing trusted-local-operator boundary, not attestation against that user.

Revalidate the pin, public snapshot and target before effects. Retained public
evidence can authorize retirement after the source daemon, private keys or scope
are gone; certificate expiry does not erase proven identity. Ambiguous provenance
or indistinguishable external effects require manual handling.

Known source/dependency observations support warnings, not complete discovery.
There is no gateway enrollment, daemon receipt writer, requirement-release
lifecycle or cross-daemon start lock. Users may acknowledge consumer impact and
proceed, potentially breaking unobserved scopes or applications; acknowledgement
cannot waive provenance. Replacement verifies the selected new root's OS trust
before retiring the old root. Standalone retirement acknowledges loss of dependent
HTTPS trust.

### Effects, receipts and recovery

Foreground commands use normal OS authorization and stock tools: exact public
certificate bytes and an explicit keychain/trust domain with macOS `security`;
a fingerprint-addressed local CA source and `update-ca-certificates` on
Ubuntu/Debian. Conflicting entries are preserved. Linux authorization includes
the normal collection refresh and configured distribution hooks, whose effects
can extend beyond one root. Backlot does not add browser-specific automation,
DNS changes, CA regeneration, privileged services or
[issue 97](https://github.com/octacian/backlot/issues/97) infrastructure orchestration.
Unavailable authorization produces actionable instructions.

Serialize foreground mutations and receipt updates with the existing file-lock
library. Store permissioned, atomic, versioned receipts and public certificates
outside daemon-state erasure in one documented account-local location per operation.
Retain only authority, exact target, intent/outcome and useful warning/recovery
observations. Unsupported writers preserve receipts unchanged. This adds neither
daemon bbolt migration nor a background registry, scanner or collector; bbolt
transactions never span OS authorization.

Journal intent before effects and observed outcomes afterward. Verify results:
stock success alone is insufficient. Report interrupted/denied operations as
partial or unknown, and distinguish trust withdrawal, entry deletion and residual
trust in other stores/domains. Cooperative locks and exact pre/post checks do not
exclude unrelated administrators; effects assume an operator-controlled maintenance
window, with manual fallback for uncertain targeting. Drift stops further
destructive effects; detecting it afterward cannot undo an external race.

Receipt loss, corruption, unsupported versions or known restoration leave status
and gateway start usable. Preserve damaged evidence; restore compatible receipts
or explicitly start clean tracking for new validated installations. Neither fresh
tracking nor reconciliation invents old removal authority. Legacy/manual installs
remain observable with safe installation guidance and manual OS recovery. Some
old roots may therefore require deliberate manual retirement.

### Fresh verification

Installation/replacement proof is root-specific in the selected OS domain,
independent of certificate-file overrides. File presence, a supplied-root pool,
override-only acceptance or TLS through another root is insufficient. Separately
report daemon-effective TLS under its retained environment, preserving overrides
and providing mismatch guidance. Browser and application trust remain independent.

Use bounded, joined, unprivileged invocations of the same executable for fresh
observations in both contexts, shared with publication readiness, doctor system-TLS
and daemon HTTPS probes. Preserve hostname/date/chain checks, owned destinations,
publication markers, redirects, cancellation and readiness semantics. Explicit-root
diagnostics remain labeled separately. A new transport or clone of
[Go's cached SystemCertPool](https://pkg.go.dev/crypto/x509#SystemCertPool) cannot
refresh Linux roots; macOS normally verifies live, but Go 1.27 file overrides can
also cache roots. Fresh processes reuse Go policy rather than maintaining another
root loader or bypassing TLS.

### Consequences

Receipts add durable foreground ownership/recovery metadata and stock-platform
adapters, with no new library dependency or work while unused. Best-effort warnings
can miss consumers; lost authority can leave manual cleanup. These limits avoid
making ordinary gateway use depend on a second coordinated state system.

Targets remain macOS ARM64, Ubuntu 24.04 amd64 and Debian 13.4 amd64. Stock-tool
source/help establishes plausibility; owned platform evidence must establish actual
trust effects, recovery and fresh verification before delivery claims.
