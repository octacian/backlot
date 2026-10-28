---
title: 'Use Windows job-object supervision and managed HTTPS'
date: '2026-10-18'
status: deprecated
summary: 'Deprecate the unimplemented native Windows design because demand does not justify its platform maintenance and verification cost.'
tags: [windows, lifecycle, ownership, docker, https, trust]
supersedes: []
superseded_by: []
---

# Use Windows job-object supervision and managed HTTPS

Deprecated on 2026-10-28: native Windows releases and runtime support are outside
project scope. Maintainer-assessed demand does not justify the separate IPC,
filesystem, supervision, provider and desktop verification work alongside macOS
and Linux. No native Windows support shipped. The original decision below is
retained as history, not implementation guidance; see the current
[platform policy](../platforms.md). Reconsider only with demonstrated demand and
maintainer capacity for implementation and ongoing native Windows testing.

## Historical overview

Use an outside guardian holding a private unnamed Windows job object, with
authenticated durable control and bounded cleanup, plus explicit local Docker
Desktop Linux-container and native stock Caddy integration. The
[Windows handoff](../windows-design.md) defines concrete rules and validation for
#37/#38. Accepted on 2026-10-18 following maintainer review of the Windows design.
This Windows-only extension does not supersede the accepted cooperative Unix
supervision or Linux HTTPS decisions. Windows execution remains unimplemented
and unverified by this change; architecture acceptance does not waive runtime gates.

## Context

Windows native/listener adapters are unsupported; current guardian control,
process-group signaling and descriptor inheritance assume Unix. Docker validation
accepts Unix endpoints and its bind inspection includes macOS-specific translation.
Windows needs original exits, disposable leases and recovery authority without
PID/name/port cleanup. The bounded target includes Windows 11 x64, PowerShell 7,
Edge, stock Caddy and Docker Desktop Linux containers from the outset.

## Decision

Configure a guardian-retained non-breakaway job with kill-on-last-close and
completion tracking before root creation; prevent job-handle inheritance. Use
creation-time association with `PROC_THREAD_ATTRIBUTE_JOB_LIST` in `STARTUPINFOEX`
and `CreateProcessW` using `EXTENDED_STARTUPINFO_PRESENT | CREATE_SUSPENDED`.
Retain attribute storage/job authority through the call, verify membership through
returned handles, and only then resume. On setup/creation/membership/resume failure,
abort activation and terminate/wait any returned child through retained authority.
No create-then-assign compatibility fallback is allowed.

A suspended create followed by `AssignProcessToJobObject` leaves a crash interval:
parent termination does not kill children and job closure affects associated
processes only. An unassigned suspended root can survive guardian loss outside
kill-on-close coverage. This is an inference from Microsoft's termination/job rules,
not a runtime observation. Microsoft documents job-list attributes on Windows 10/
Server 2016 and newer. Creation-time assignment removes the user-space interval,
but documentation alone does not establish every in-call crash/partial-failure
outcome. #37 must review and demonstrate that boundary before adapter acceptance;
otherwise return for a reviewed retained-authority design. If a child may exist
without retained authority, name the possible suspended unassigned residual,
quarantine the journal/state, block conflicting restart and report cleanup failure.
No failed create, guardian exit or closed port proves absence.

Require the [deterministic startup fault gates](../windows-design.md#deterministic-startup-fault-acceptance-37):
a reproduction-only two-step fixture crashes after creation/before assignment and
exposes the surviving root; the creation-time adapter crashes after association/before
resume and must demonstrate bounded root termination without activation. Before
either fault an independent controller acknowledges retained root termination/wait
and guardian launch handles and registers cleanup; it retains no owned-job handle.
Fixture disposal cannot become a product cleanup receipt. In-call death before
handles return needs separately retained cleanup authority (such as granted disposable
VM teardown), not an observed PID; absent that authority/evidence the gate is blocked,
the boundary remains unproven, and #37 acceptance requires further review.

Retain root status and collectors independently. Journal private generation/control
capabilities before activation. Recover through the exact authenticated guardian,
never by reopening a job name or signaling an observed PID. Guardian loss remains
uncertain without authenticated completed cleanup proof, even with kernel kill-on-close
for associated processes; preserve diagnostics and do not resume/retry work.
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

Create-suspended then assign-to-job is rejected as a production fallback because
guardian death can strand an unassigned child; suspension is not cleanup authority.
Creation-time job lists avoid that user-space interval but require #37 crash evidence.
A second runtime owner retaining root authority would need a reviewed handoff and
failure contract; duplicating a job handle also delays kill-on-last-close.
Taskkill tree/PID enumeration has ownership and fork/reuse races. Named-job reopen
from journal text is not retained authority. Leader-only stop loses descendants.
A privileged service adds installation and authority beyond this baseline.
CTRL_C broadcasting can affect the user's console and cannot target a chosen group.
Automatic trust/runtime detection or exported CA directories weaken existing
contracts. WSL/cross-build/macOS fixtures cannot substitute for native Windows
execution. An explicit wider HTTPS bind is a reviewed fallback only if actual
Desktop evidence requires it; private admin never widens.

## Revisit when

Creation-time association/crash evidence, actual Windows console/nesting tests,
canonical DNS, public-file mount permissions or Desktop networking cannot meet this
design; a native GUI/service workload is required; or the operator changes backend,
Engine or trust policy. Record alternatives and seek consequential product
decisions before dependent changes.

## References

- [Windows implementation and acceptance handoff](../windows-design.md)
- [Accepted native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Accepted lifecycle](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Accepted HTTPS boundary and retained decisions](20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
- [Microsoft jobs, handle lifetime and descendant caveats](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)
- [Microsoft creation-time job-list attributes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute)
- [Microsoft parent/child termination rules](https://learn.microsoft.com/en-us/windows/win32/procthread/terminating-a-process)
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
