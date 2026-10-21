---
title: 'Allow cooperative native process groups within an owned session'
date: '2026-10-21'
status: accepted
summary: 'Allow ordinary native watchers to create process groups inside a live guardian-owned session, with bounded identity-safe cleanup and explicit residual-process risk.'
tags: [native, lifecycle, ownership, watchers, macos, linux]
supersedes: [20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md]
superseded_by: []
---

# Allow cooperative native process groups within an owned session

Replace the single-process-group restriction with a cooperative Unix
session boundary for application native services, jobs and readiness commands.
Keep Backlot-managed system helpers, including stock Caddy, on strict group
supervision and its existing stronger absence contract. An internal launch purpose
selects these two bounded policies; application executable names never select it.
Ordinary Air and `npm run dev` commands remain application inputs; Backlot adds no watcher or
application restart loop. A live authenticated guardian authorizes session
membership; guardian-loss witnessed recovery is a deferred alternative. Signal
individually acquired kernel process identities, with bounded cleanup scans and an explicitly weaker, observed-empty cleanup result.

This decision supersedes the [former native supervision
ADR](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md).
Following independent review and supported-platform adapter evidence, acceptance
explicitly permits observed-empty cleanup to release stop/restart/output/data-deletion
fences despite unseen compliant-fork risk, while retaining guardian-loss uncertainty.
Witness recovery, a public risk acknowledgement and a second keeper remain outside
this minimum scope. Acceptance establishes the contract, not completed integration.

## Context

[Issue #49](https://github.com/octacian/backlot/issues/49) reproduces a watcher
application listening in a different process group. Listener validation correctly
rejects that application under the existing cleanup boundary. Merely accepting
its listener would leave stop unable to reach it. The owner subsequently approved
general support for multiple process groups within an owned session, preserving
normal Air and npm workflows, with modest cleanup or a deliberate weaker guarantee
instead of privileged containment or fragile tracking.

External feasibility evidence at baseline `2bfc9b4078c180e88e80887808100f5139ea7915`
on macOS 27.0.1 ARM64 used Air v1.67.4 and purego v0.11.2. An unreaped direct
session-leader fixture pinned its session: normal Air TERM left no observed live
members; killing Air left an application reached by an audit-token signal. A
separate CGO-disabled probe rejected an altered audit token and signaled the
correct owned child. Ordinary npm/Vite 8.3.4 already starts and serves edits under
the current group contract. These are feasibility observations, not guardian-loss,
fork-churn, Linux, supported-version or gateway/browser HMR acceptance. The
revision-bound author report identifies the external raw evidence and commands;
no private capabilities belong in repository documentation.

Current `native.Start` creates the guardian with `setsid`; the guardian is already
the session leader. It launches a separate group anchor and root inside that
session. Its live PID pins the numeric session ID during its own cleanup. The
current anchor pins only its group in the implemented protocol: its identity is
not durably recorded before root launch. XNU PID allocation also excludes existing
session IDs, so a surviving authenticated member could witness the original
session after leader death. That motivates the narrower anchor-witness option
below; a stale numeric SID alone still cannot distinguish a reused session.

A token/pidfd prevents signaling a replacement process after identity acquisition;
it does not prove the acquired process was a member at the right time. Session
membership and enumeration are separate, racy observations. Public macOS process
events do not provide recursive fork enrollment, and a finite scan cannot prove
that no fork occurred behind its cursor. Strong complete-session absence would
need another mechanism and a different cost decision.

## Decision

### Ownership and listener boundary

Keep private control capabilities, inactive-before-journal activation, retained
logs, root exit accounting, deadlines and explicit restart semantics. Use the
live guardian's own session ID for the attempt. The guardian excludes itself and
its retained group anchor from workload-member cleanup; helper exclusion is by
retained authority, never name or arbitrary PID allowlists. Descendants may change
groups, but must remain in this session and preserve ordinary same-user signal
permissions. `setsid`, external supervisors, privilege changes and hostile escape
remain unsupported; this is not a sandbox or arbitrary descendant guarantee.

Session-sensitive operations run inside the guardian while it is executing, so
its own session ID remains allocated throughout those operations. Daemon-side
listener checks request a bounded authenticated membership observation from that
authority for application-session launches; strict helpers keep group checks.
Persisted SID or PID values alone are never authority. Preserve existing
loopback resolution, every relevant listener's ownership, exact managed-gateway
address/family checks and unrelated-port refusal. Match the lsof listener PID to
a current kernel identity and revalidate membership and identity around that
observation. A disappearing/replaced listener means retry/not-ready, not an
unrelated-PID signal or successful ownership. Descriptor/network observations
still are not atomic with the eventual connect; this proposal does not promise
hostile same-user listener exclusion.

Keep the pinned-group mechanism for initial root/anchor launch. Strict system
helpers retain its existing strong group cleanup. Session-mode application work
uses individual identity signals, excluding the retained anchor from
TERM/KILL until the final release phase. This keeps one simple session scan policy
and permits later evaluation of the witness alternative without group broadcasts.
Add only session enumeration, identity acquisition/liveness,
and individual signaling to the OS boundary. Do not send a negative-PGID signal to
an enumerated additional group. There is no continuously maintained member graph,
process-name dispatch, launch wrapper requirement, event subscription, or global
process cleanup worker.

### Identity acquisition and races

On macOS acquire a task-name right with `task_name_for_pid`, obtain
`TASK_AUDIT_TOKEN`, validate returned count/PID and release the Mach right on all
paths. Use `proc_signal_with_audittoken` only for actual signals: XNU rejects signal
zero. Reacquire and compare the token after membership reads; stale/missing tokens
require discarding the candidate. Do not substitute kill(PID, 0) for identity.
On Linux acquire a close-on-exec pidfd with
`unix.PidfdOpen` and signal only with `unix.PidfdSendSignal`; close every descriptor.
Do not serialize handles for later reacquisition as a cleanup authority.

For an enumerated candidate, acquire its identity first, read its current session
membership, then verify that the acquired identity still names the same current
process before signaling it (a second matching token on macOS; pidfd signal-zero on Linux). This
post-read validation prevents accepting membership from a recycled PID: the
original process still occupied that PID across the membership read. An exit or
replacement after validation is handled by kernel-checked actuation. Verify this ordering on both kernels before relying
on it. Recheck membership immediately before actuation; the cooperative no-session-
escape rule bounds the remaining membership-to-signal race. Bound acquisition retries to at most three attempts within the
remaining operation deadline; exhaustion is incomplete, not absence. Exec does not
authorize a replacement identity: changed/stale audit tokens are reacquired and revalidated,
never downgraded to raw PID signals. Credentials/permission changes may cause a
bounded cleanup failure even for a previously observed member.

A vanished candidate is normal scan churn, not global absence. Denied identity
acquisition, unreadable membership, unsupported APIs, malformed responses or
resource exhaustion prevent an observed-empty result if a possible member cannot
be ruled out. Unrelated processes need no task handle; an initial read-only
membership filter may reduce acquisitions, but the post-acquisition validation is
still mandatory. No error-to-empty conversion, broad user-wide signal, PID
birth-time check followed by kill, or best-effort raw-PID fallback is permitted.

### Bounded cleanup and confidence

Within the existing stop grace, send TERM to validated workload members. The
anchor remains live and is excluded from these member signals. Newly observed identities receive TERM while grace remains;
after grace, repeated bounded scans and identity-checked KILL cover newly found
members. Cap each pass by the verification deadline and a finite candidate/handle
budget; an exceeded budget is incomplete cleanup. Keep existing finite collector
and guardian-join deadlines; no unbounded retry or hidden expiry worker.

After escalation require two successful complete scans separated by a short
interval (initial value 20 ms), finding no live workload members, and confirm all
acquired identities are exited or zombies. A zombie does not execute or hold
application descriptors, but still must not authorize raw-PID signaling. Retain
the anchor through the session scans. Then terminate it through retained direct-child
authority (guardian) or validated identity (recovery), with no later SID-based
actuation. Preserve the group-absence check for its original group; it does not
prove absence of other groups in the session. Close scans before releasing handles, join root/status/output
owners, persist an authenticated cleanup receipt, and verify guardian exit.
Collector failure stays separate from process observation and makes the overall
operation nonzero.

Call successful session cleanup **observed empty**, never complete descendant
absence. Two scans improve practical confidence but cannot exclude an unseen
fork racing the final pass, or an unsupported session escape. The owner must
explicitly accept that a process may remain after a successful stop, restart or
owned-data deletion even with this result. Successful cleanup may release the
ordinary lifecycle/data fences under that accepted risk; strengthening absence
must not be smuggled back in as an impossible release gate. Positively observed
live members, denied checks, lost authority, exhausted budgets and missing joins
remain incomplete, nonzero, and fenced.

Expose a named shared native cleanup result per attempt/component with confidence
`observed_empty`, `incomplete`, or `authority_lost`, plus a stable reason code and
bounded non-secret detail. Do not call observed-empty a cleanup failure, but show
its residual-process limitation in human output, JSON status and exports. Preserve
original job status, collection failures and existing structured cleanup errors.
An acknowledgement of risk is a separate recorded operator decision, not another
confidence value and not proof of cleanup.

### Daemon recovery and guardian loss

Daemon crash with a surviving guardian reuses the same authenticated control
protocol and bounded cleanup; interrupted logs/status remain gaps and no workload
resumes automatically. The guardian remains the session owner across daemon
replacement. Authenticate requests and receipts against attempt, protocol version
and original guardian identity; old or missing authorities cannot authorize new
session scans. A private receipt produced before guardian exit can attest its
recorded observation, subject to its weaker confidence contract.

Under the current protocol, guardian crash, missing control, identity mismatch
or lost receipt supplies no safe new session authority. The proposed witness
option below can narrow ordinary guardian-loss cases only after its identity
effect is journaled and its lifetime argument is verified. Do not enumerate or
signal against a saved SID alone, respawn
a helper into the supposed old session, or interpret root/listener absence as
cleanup. Persist `authority_lost`, retain affected data and output/instance fences,
and keep unrelated daemon operations available. Recovery does not block the
whole daemon waiting for this instance and does not retry forever.

Recommend retaining the current per-instance authority-loss policy for the
minimum implementation on both platforms. Ordinary stop and daemon-crash recovery
with a surviving guardian are required. Guardian-crash recovery is not a new
guarantee or a prerequisite to application-session support. The reviewer should
weigh the following optional strengthening against its full cost; none is required
by this proposal's minimum scope:

- **Deferred strengthening: reuse the existing anchor as session witness.**
  Keep the existing helper count and root/stream ownership. Add an activation
  barrier: start anchor, obtain kernel identity plus session, persist that effect,
  acknowledge it, then let the guardian start root. Withhold root on any failed
  barrier. Retain the anchor until all session cleanup, including escalations, is
  complete; additional session cleanup must not kill it via a group broadcast.
  After guardian loss, the daemon can acquire the exact persisted anchor identity,
  verify its original session, acquire candidate identities and membership, then
  verify the witness is still the same live process after those reads. Only then
  signal those acquired identities. Revalidate the witness for every new batch;
  a single startup check is insufficient. If the witness dies before validation,
  discard the batch: its SID could have been reused. A candidate validated while
  the witness was alive is itself an owned identity even if the witness dies
  before signaling; never retarget its numeric PID. This is finite recovery using
  the same OS adapter, not a permanent membership tracker or second cleanup engine.
  Linux durable authentication may still add a liveness protocol, as bounded below. Prove live-member SID pinning on Linux as well as XNU, audit-token
  reacquisition/exec semantics, anchor permission/reaping behavior after parent
  loss, and the new pre-root persistence barrier before implementing this path.
  Ordinary guardian crash can then be recoverable, but anchor loss, both-helper
  loss, and a crash after final anchor termination before receipt recording still
  lose authority. For those residual cases retain the existing nonzero diagnostic
  and per-instance/data/output fences. Do not add a public acknowledgement API in
  the initial scope, nor make host reboot a routine prerequisite.
- **Deferred alternative: explicit per-instance risk acknowledgement.** After operator
  inspection, a named typed operation bound to instance, attempt and current
  diagnostic revision may retire the unresolved native obligation for *launch
  eligibility* only. Record the acknowledged risk and original failure durably;
  never erase them or return a successful stop for that attempt. Refuse if a
  retained/authenticated identity positively proves a live consumer; unknown is
  explicitly still unknown. Permit a later explicit restart and release declared
  checkout-output fences only through this separate acknowledgement. Preserve
  owned data by default; acknowledgement alone never authorizes reset/destroy
  deletion, container cleanup, or gateway ownership override. Such deletion still
  needs cleanup evidence or independent proof old processes cannot survive.
  This alternative requires a further owner decision before implementation.
  Diagnostics must explain that an unknown stale process can race the restarted
  workload or write its retained data. This adds a public operation, durable
  operator-decision record and eligibility changes; it is not a free flag.
- **Larger alternative: surviving session keeper.** Move session leadership and
  root launch into the existing anchor helper, keeping the outside guardian as
  its authenticated client, with independently recoverable control and receipts.
  This is a second cleanup authority with activation ordering, EOF/crash handling,
  root/stream forwarding, independent identity journaling and two-helper join
  obligations. It can survive loss of the outer guardian but not all keeper
  failures. Do not implement it without a separately reviewed scope checkpoint.

A future verified host boot-generation change could provide secondary process-
non-survival proof for native obligations. It is not required or implemented by
this minimum proposal; persisting it would need its own proportionality check.
No mandatory reboot-only workflow, auto-forget TTL, arbitrary-PID kill instruction
or general force-delete mode is introduced. Retained uncertainty is an existing
rare-failure boundary, not a claimed repair guarantee. Do not expand the minimum
implementation into witness recovery, acknowledgement
or a tracking framework. If retained guardian-loss uncertainty is unacceptable in
review, decide explicitly between those options and the narrower cooperative
watcher fallback before dependent changes. Operational recurrence of stranded instances reopens this tradeoff.

### Implementation scope and compatibility checkpoint

The concrete implementation boundary is:

| Area | Required work and limit |
| --- | --- |
| `internal/native` | Thin Darwin/Linux identity/session adapters; guardian membership request; bounded cleanup; versioned identities/receipts and confidence; finite handles/rights; keep current root exit/collection behavior. No new permanent tracker. |
| Durable state | Advance private state from v5 to v6, rejecting older populated state without mutation or migration. Record supervision/protocol kind, attempt and confidence in application identities/results and receipts. Keep capabilities private. No witness identity journal, extra pre-root barrier, boot generation or acknowledgement record in minimum scope. |
| Writers | Application launch intent/effect and result writers, readiness-command launches, runtime cleanup/recovery and diagnostic/export writers must agree. Managed Caddy launch/recovery explicitly selects strict-group mode and keeps existing absence semantics; test that the shared supervisor does not change its policy. |
| APIs and CLI | Named shared cleanup result, strict decoding/validation in `api/v1`, daemon/client/CLI/status/doctor/export rendering and contract tests. Existing cleanup string/details remain truthful. No new endpoint in initial scope; acknowledgement is a separate future API decision. No endpoint accepts PID, SID, token or a generic force flag. |
| Concurrency | Existing instance lifecycle lock serializes stop/restart/recovery; guardian serializes scans/activation/stop. Export fences and evidence pruning retain unresolved diagnostics. Known live consumers continue blocking destructive lifecycle actions. |
| Background ownership | Existing guardian owns scans only during readiness/stop; existing daemon execution owns control/status collection and cancellation/join. No polling when native work is idle, container-only, offline or disabled. Recovery is finite per recorded authority; no new scheduler. |
| Dependencies | Candidate purego v0.11.2 only on Darwin; existing x/sys pidfd APIs on Linux. Add dependency/notices only with implementation. No cgo requirement, helper installation, privileged service, cgroups or process tracking framework. |
| Defaults and failure | Session behavior becomes the default for application native launches, including readiness; internal system-helper launches retain strict group mode; no watcher-name switch or user feature flag. Check required platform capabilities before application activation; unavailable session identity APIs fail native activation explicitly. Container-only/offline work must not load Darwin FFI or probe host processes. Do not silently downgrade to group-only behavior. |
| Operations/docs | Old binary cleans/stops v5 resources before upgrade to a fresh v6 state directory, following current no-migration policy. Update PRD, architecture/native/platform guides, implementation tracking and acceptance wording after acceptance; retain original ADR rationale. Explain observed-empty cleanup and bounded guardian-loss uncertainty; no new operator action, trust/DNS/system-service changes. |

Purego preserves CGO_ENABLED=0 builds and lists Darwin ARM64 as tier 1, but labels
itself beta. It introduces dynamic symbol loading, unsafe ABI bindings, Mach
resource lifetime and Go-runtime compatibility maintenance; static Go build does
not mean no system dylib dependency. Pin its version, use fixed system library
paths, resolve symbols with returned errors rather than process panic, and test
struct layout/count, stale token rejection, permission failures and rights leaks.
It is preferred over handwritten assembly/runtime linkage or requiring cgo for
one small adapter, conditional on actual supported-platform proof.

The local SDK declaration does not establish a minimum macOS release. Only macOS
27.0.1 has probe evidence; existing CI runs macOS 15. Require macOS 15 ARM64 real
runtime proof of same-user task-name/token signaling with shipped CGO-disabled
build and race build before choosing a minimum version. If APIs or permissions
fail there, return for a baseline/dependency decision; do not silently drop the
existing target. Linux pidfd APIs are feasible by documented interface and existing
x/sys, not yet runtime-proved here; require Ubuntu 24.04 and Debian 13.4 evidence,
including denied/syscall-unavailable behavior. Seccomp/proc restrictions may fail
native activation/cleanup explicitly. Windows job supervision remains unchanged.

### Bounded acceptance

For the deferred witness alternative, Linux durable witness recovery is a distinct
feasibility gap; it does not block the minimum live-guardian implementation. A numeric PID plus
`/proc` start ticks is not a kernel-unique persisted token; opening a pidfd later
makes subsequent signals safe for that acquired process but does not by itself
authenticate it as the old anchor. Do not treat check-starttime-then-pidfd-open as
proof. Evaluate a narrow private challenge/response served by the existing anchor
(binding the response to its own live session and retained identity) with
peer/pidfd validation, or another documented kernel identity facility available
on both supported distributions. That would add an anchor liveness protocol,
private capability/path, publication/cleanup and join obligations even though it
does not make the anchor a second cleanup engine. Return for scope review if this
is required; do not silently describe it as a few pidfd calls. Do not introduce platform-specific recovery promises without explicit review.
The minimum design retains guardian-loss uncertainty on both platforms.

First prove live-guardian identity/membership ordering and stale-target rejection on macOS 15 ARM64 and Linux amd64 before broad
integration. Use exclusive temporary owned
fixtures, retained cleanup authority before every fault, dynamic loopback ports
and self-expiration when authority may be deliberately lost. Report denied APIs,
resource limits and residuals; never clean up by observed PIDs.

Then run an early real CLI/daemon Air rebuild plus ordinary npm/Vite flow, with
listener ownership and configured gateway HMR, stop/cancellation and unrelated
sentinel preservation. Add finite fork/exec/group churn, watcher/root exit,
collector timeout, denied identity, stale receipt, daemon-loss and guardian-loss
cases. Guardian loss must preserve uncertainty without affecting unrelated
instances. Exercise per-instance/output/data fences, restart refusal and
persistence across another daemon restart; witnessed cleanup is not a gate.
These tests sample missed-process risk; they cannot prove universal absence.

Run `make check`, shipped CGO-disabled Darwin/Linux builds, notices and applicable
native/managed-gateway/provider/browser regression suites on the frozen candidate,
with exact revision/tool/platform provenance and cleanup reports. Use the existing
supported-platform evidence policy, not a new broad watcher compatibility matrix.
No Docker or shared trust/DNS grant is implied by this ADR. Architecture authoring
checks establish only document validity; independent architecture review precedes
production changes and frozen runtime acceptance follows implementation.

## Consequences

Ordinary child-group watchers fit the native contract without replacement tooling,
privileged installation or a recursive process tracker. Kernel identities bound
to validated membership prevent PID-reuse actuation. Small OS adapters remain
possible, but listener/control, recovery, public confidence and private-state
compatibility are cross-cutting changes rather than a listener predicate patch.

Successful teardown has weaker absence confidence than the former pinned-group
contract. An unseen live descendant can race restart or data cleanup. Guardian
loss retains the current authority-loss boundary, preserving data and fences.
Deferred witness recovery needs fewer helper roles than a keeper, but Linux durable
authentication is unresolved; a keeper adds another authority protocol and maintenance cost.
Neither option gives hostile containment or guaranteed fork coverage. Choosing a different tradeoff requires revising this proposal before
implementation rather than weakening diagnostics later.

## Alternatives considered

- Keep the current group contract and use cooperative Watchexec configuration:
  demonstrated feasible, smallest implementation, and retains stronger group
  absence. It requires changing normal Air workflow; owner prefers general
  session support unless that becomes disproportionate. It remains the fallback
  if platform identity APIs or recovery costs fail review.
- Accept same-session listeners but clean only the original group: leaves observed
  Air children beyond stop reach and silently breaks lifecycle/data assumptions.
- Poll ancestry, names or environment markers: cannot authorize actuation after
  reparenting/PID reuse, and is neither a substitute identity nor complete coverage.
- Always require complete session absence or guardian-crash cleanup: enumeration
  cannot deliver it; an extra keeper still has a final owner-loss boundary. Do not
  grow this request into a privileged service, cgroups, Endpoint Security or a
  generic tracking system to preserve an inherited stronger promise.
- Keep all guardian-loss fences until reboot: simple and safe for deletion but an
  excessive sole recovery workflow under the owner's stated preference. Boot
  proof remains a possible secondary mechanism. Retain current per-instance
  uncertainty in minimum scope; consider witness recovery or acknowledgement if
  recurring failures make that boundary unsuitable.
- Require cgo for libproc: conventional bindings, but changes the shipped static-Go
  build/cross-build contract. Compare again if purego fails actual ABI/support gates.
- Retain strict-group supervision for Backlot-owned system helpers: chosen because
  they do not require child-group watcher compatibility. Sharing code does not
  justify weaker gateway cleanup or a public supervision-mode switch.

## Revisit when

Same-session unseen survivors occur in ordinary supported watchers; retained
authority uncertainty leaves recurring unusable instances/data; supported macOS/Linux
APIs cannot provide identity-bound actuation without privilege; purego/runtime
upgrades break bindings; or actual adopters require new sessions, privilege changes
or hostile containment. Any stronger absence/recovery promise needs new evidence
and an explicit lifecycle-cost decision.

## References

- [Issue #49 and original scope](https://github.com/octacian/backlot/issues/49)
- [Current native supervision](20261008T004039730Z-supervise-cooperative-native-process-groups-with-durable-guardian-identity.md)
- [Native authority implementation](../../internal/native/authority.go)
- [Native launch/identity](../../internal/native/group.go)
- [Listener ownership](../../internal/native/listener_unix.go)
- [Runtime journal/recovery and output fences](../../internal/daemon/runtime_state.go)
- [Native execution guide](../daemon.md#native-execution)
- [Platform policy](../platforms.md)
- [Windows boundary](../windows-design.md#windows-owned-runtime-37)
- [Air v1.67.4 group creation](https://github.com/air-verse/air/blob/v1.67.4/runner/util_unix.go)
- [XNU audit-token signaling implementation](https://github.com/apple-oss-distributions/xnu/blob/f6217f891ac0bb64f3d375211650a4c1ff8ca1ea/bsd/kern/proc_info.c)
- [XNU session-aware PID allocation](https://github.com/apple-oss-distributions/xnu/blob/f6217f891ac0bb64f3d375211650a4c1ff8ca1ea/bsd/kern/kern_fork.c)
- [XNU process identity implementation](https://github.com/apple-oss-distributions/xnu/blob/f6217f891ac0bb64f3d375211650a4c1ff8ca1ea/bsd/kern/kern_proc.c)
- [Linux pidfd_open contract](https://man7.org/linux/man-pages/man2/pidfd_open.2.html)
- [Linux pidfd_send_signal contract](https://man7.org/linux/man-pages/man2/pidfd_send_signal.2.html)
- [Purego v0.11.2 support and caveats](https://github.com/ebitengine/purego/tree/v0.11.2)
