---
title: 'Propose Windows owned runtime and managed HTTPS'
date: '2026-10-18'
status: proposed
summary: 'Propose retained Windows job-object authority and explicit Docker Desktop and Caddy integration while requiring native runtime and desktop adoption evidence before support.'
tags: [windows, lifecycle, ownership, docker, https, trust]
supersedes: []
superseded_by: []
---

# Propose Windows owned runtime and managed HTTPS

Propose an outside guardian holding a private unnamed Windows job object, with
authenticated durable control and bounded cleanup, plus explicit local Docker
Desktop Linux-container and native stock Caddy integration. The
[Windows handoff](../windows-design.md) defines concrete rules and validation for
#37/#38. This record is proposed only; accepted cooperative Unix supervision and
Linux HTTPS decisions are unchanged, and Windows execution is not verified.

## Context

Windows native/listener adapters are unsupported; current guardian control,
process-group signaling and descriptor inheritance assume Unix. Docker validation
accepts Unix endpoints and its bind inspection includes macOS-specific translation.
Windows needs original exits, disposable leases and recovery authority without
PID/name/port cleanup. The bounded target includes Windows 11 x64, PowerShell 7,
Edge, stock Caddy and Docker Desktop Linux containers from the outset.

## Decision

Create each root suspended, assign it to a guardian-retained non-breakaway job
before activation, and prevent job-handle inheritance. Configure kill-on-last-close;
retain root status and collectors independently. Journal private generation/control
capabilities before activation. Recover through the exact authenticated guardian,
never by reopening a job name or signaling an observed PID. Guardian loss remains
uncertain without authenticated completed cleanup proof, even with kernel kill-on-close.
Use isolated-console targeted BREAK for cooperative grace, then retained-job
termination and separately bounded job/root/collector/guardian joins. Require real
nested-job, console, descendant and crash tests before accepting this adapter.
Use owner TCP tables plus retained process identity/job membership for readiness
observations, never as termination authority. Preserve all existing lease/results,
stop/reset/destroy, explicit restart and uncertainty semantics.

Use executable/argument arrays and explicit shells, Windows-aware Unicode quoting,
case-insensitive environment-key validation and separate Windows-host/Linux-target
path handling. Preserve explicit trust and daemon-start environment inheritance.

Use only explicitly configured local Docker named pipes with the official SDK;
verify Linux Engine, exact owned labels/IDs, actual bind representations and public
file bytes. Do not infer contexts or normalize unverified Desktop VM prefixes.
Native-only selection remains Docker-independent. Mount only the existing public
CA file read-only; no private parents, key exposure or permission relaxation.

Run stock native Caddy under the guardian with persistent pinned CA/saved config,
private loopback admin and existing scoped route/ETag ownership. Keep canonical
origins, loopback HTTPS defaults, explicit Engine host-gateway mappings and explicit
application trust. Prove Windows native DNS, Desktop forwarding and readonly trust
mounts. Fail and review incompatible topology rather than adding automatic widening,
DNS rewriting, trust injection or TLS bypasses. System/Edge trust is a separate
operator stock-Caddy action with exact store/fingerprint and desktop evidence.

## Consequences

Handle-based job authority can avoid Unix PID/group assumptions and bound ordinary
Windows descendants, but is not a hostile-workload sandbox. Console and nested-job
compatibility, provider mount identity, normal DNS and Desktop reachability remain
unproven acceptance gates. No runtime/CI/dependency/service or trust changes occur
in this design slice. Windows Server CI cannot establish Windows 11 Edge adoption.
The epic closes only after complete native/provider/desktop evidence, independent
reviews and published-HEAD CI; #37 alone must not advertise complete support.

## Alternatives considered

Taskkill tree/PID enumeration has ownership and fork/reuse races. Named-job reopen
from journal text is not retained authority. Leader-only stop loses descendants.
A privileged service adds installation and authority beyond this baseline.
CTRL_C broadcasting can affect the user's console and cannot target a chosen group.
Automatic trust/runtime detection or exported CA directories weaken existing
contracts. WSL/cross-build/macOS fixtures cannot substitute for native Windows
execution. An explicit wider HTTPS bind is a reviewed fallback only if actual
Desktop evidence requires it; private admin never widens.

## Revisit when

Actual Windows console/nesting tests, canonical DNS, public-file mount permissions
or Desktop networking cannot meet the proposal; a native GUI/service workload is
required; or the operator changes backend/Engine/trust policy. Record alternatives
and seek consequential product decisions before dependent changes.

## References

- [Windows implementation and acceptance handoff](../windows-design.md)
- [Accepted native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Accepted lifecycle](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Accepted HTTPS boundary and retained decisions](20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
- [Microsoft jobs, handle lifetime and descendant caveats](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)
- [Microsoft nested jobs](https://learn.microsoft.com/en-us/windows/win32/procthread/nested-jobs)
- [Microsoft CreateProcessW](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw)
- [Go Windows argument conventions](https://pkg.go.dev/os/exec#Command)
- [Go system certificate pools](https://pkg.go.dev/crypto/x509#SystemCertPool)
- [GitHub hosted runner baseline](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- [Microsoft targeted console events](https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent)
- [Microsoft owner TCP tables](https://learn.microsoft.com/en-us/windows/win32/api/iphlpapi/nf-iphlpapi-getextendedtcptable)
- [Docker local Windows endpoints](https://docs.docker.com/desktop/troubleshoot-and-support/faqs/general/)
- [Docker Windows permissions](https://docs.docker.com/desktop/setup/install/windows-permission-requirements/)
- [Docker Desktop bind mounts](https://docs.docker.com/engine/storage/bind-mounts/)
- [Docker Desktop networking](https://docs.docker.com/desktop/features/networking/)
- [Caddy local CA](https://caddyserver.com/docs/automatic-https#local-https)
- [Caddy stock trust and resume](https://caddyserver.com/docs/command-line)
- [Edge local platform trust and policy](https://learn.microsoft.com/en-us/deployedge/microsoft-edge-security-cert-verification)
- [Native slice 37](https://github.com/octacian/backlot/issues/37), [integration slice 38](https://github.com/octacian/backlot/issues/38)
