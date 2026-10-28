# Native Windows architecture and implementation handoff

Archived on 2026-10-28: native Windows releases and runtime support are outside
project scope. Both Windows ADRs linked below are deprecated because demand does
not justify ongoing platform maintenance and verification alongside macOS/Linux.
The remaining implementation work in #34/#37/#38 is cancelled, not delivered.
This document preserves the original design, links and acceptance anchors for
historical reference; its requirements are inactive. See the current
[platform policy](platforms.md), including WSL configuration guidance without a
support guarantee. Revisit only with demonstrated demand and maintainer capacity
for ongoing native Windows testing.

## Historical design

This is the accepted architecture and documentation-only design for [#36](https://github.com/octacian/backlot/issues/36),
under the independent [Windows epic #34](https://github.com/octacian/backlot/issues/34).
Windows is planned, **not supported or runtime-verified by this change**.
[#37](https://github.com/octacian/backlot/issues/37) delivers native runtime;
[#38](https://github.com/octacian/backlot/issues/38) delivers Docker/HTTPS/adoption.
Neither slice alone establishes the complete epic. Existing macOS/Linux contracts
and evidence requirements remain unchanged; WSL Backlot execution is not Windows evidence.

Baseline: Windows 11 x64 desktop, PowerShell 7, Edge, native Git and Go, stock
native Caddy, Docker Desktop running Linux containers. The user's desktop VM and
working local Linux containers are confirmed prerequisites, not Backlot test results.
Record exact OS build, PowerShell/Edge/Git/Go/Caddy/Desktop/Engine versions and Desktop
backend at runtime setup. Prefer implementation from native Windows Codex and a
Windows checkout. Windows services, Windows containers, remote engines, ARM64,
other browsers, privileged installers and broad packaging are deferred. Coordinate
ordinary binary distribution with #25 only after verification.

The maintainer accepted both Windows ADRs on 2026-10-18 after reviewing the
architecture, alternatives and unverified runtime assumptions:
[private IPC and identity](adr/20261018T210000000Z-propose-windows-private-ipc-and-filesystem-identity.md)
and [supervision and integration](adr/20261018T210001000Z-propose-windows-owned-runtime-and-managed-https.md).
These Windows-only extensions do not supersede existing macOS/Linux decisions.
Their acceptance establishes the implementation direction, not runtime support.
If implementation evidence requires a material change, obtain a reviewed decision
and follow the ADR lifecycle while retaining all macOS/Linux requirements.

## Current source and implementation boundaries

Inspected baseline: `2fa3131017188f056bb5d4cfd129e005ac83c0fc`.
This inspection is source evidence only.

| Boundary | Current source | Follow-up |
| --- | --- | --- |
| Local transport/privacy | `internal/localipc`, `internal/client/client.go`, `internal/daemon/server.go`, `control.go`, `files.go` | #37: replace Windows owner rejection/Unix dialing and stale-socket assumptions with OS adapters; retain typed `api/v1` |
| Checkout identity | `internal/daemon/identity.go`, `identity_windows.go`, `internal/plan/source.go` | #37: Windows currently returns unsupported; add handle-derived identity and discovery equivalence |
| Process/authority/listener | `internal/native/group_other.go`, `listener_other.go`, `authority.go`, `group.go`; `internal/daemon/process_windows.go` | #37: unsupported/no-op Windows adapters; separate Unix-only guardian assumptions including descriptor 3, group signals and Unix control paths |
| Endpoint and mounts | `internal/manifest/validate.go`, `internal/daemon/docker.go`, `container_trust.go`, `internal/plan` | #38: Unix-only endpoint validation/client setup and Darwin-specific `/host_mnt` overlap handling need Windows branches |
| Caddy/trust | `internal/gateway`, `internal/daemon/gateway.go`, `container_trust.go` | #38: retain saved config, CA pin, scoped route intents and ETags; Windows supervisor/listener integration |
| Examples/CI/docs | `examples/container-https`, `examples/release-acceptance`, `.github/workflows/check.yml`, `docs/testing.md` | #37 native fixtures/CI; #38 runnable Windows example, provider CI and actual Edge walkthrough |

Audit dependencies at implementation time, including the pinned Docker SDK's
named-pipe transport, Windows bbolt lock behavior and any required Win32 bindings.
Prefer maintained Go adapters (evaluate Microsoft go-winio and golang.org/x/sys/windows
against needed ACL/cancellation/handle features); do not add dependencies in #36.
A library default ACL or default endpoint is not the product security policy.

## Windows private transport, state and identity (#37)

Keep HTTP/JSON `/v1` framing, shared named types, version rejection, bounded bodies,
streaming and structured errors. Transport is a local byte-mode named pipe, not
TCP. Derive a short deterministic pipe locator from the current user SID and
handle-canonical state-root identity, with a versioned namespace; put no raw path
or capability in the name. Use `PIPE_REJECT_REMOTE_CLIENTS`, explicit protected
DACLs and `FILE_FLAG_FIRST_PIPE_INSTANCE` for the initial server instance. All
additional listening instances must belong to that retained server generation.
Reject occupied/foreign pipe names; never disconnect or replace their server.

Grant only the owning user SID the required client/server rights; use explicit
pipe rights rather than accidentally granting instance creation through generic
write. Validate the connecting user's token, and have the client verify server
identity against private generation metadata and an authenticated handshake before
sending sensitive requests. Bind each handshake to a fresh nonce, API version,
state-root identity and random generation capability; no replayable public token.
Use HMAC-SHA256 challenge/response with a private 32-byte generation key,
distinct client/server domain tags and both nonces; never transmit the key. Bound
handshake time/size and reject replay, wrong generation and version mismatch.
A SID boundary is per-user across sessions, not a hostile same-user sandbox.
Administrators/SYSTEM can override user security; do not promise isolation from them.
Do not require elevation or broaden ACLs to fix access denied.

Preserve the existing `os.UserConfigDir()/backlot/daemon` default and explicit
absolute `--state-dir`. Require a local NTFS directory supporting the identity/ACL
contract. Reject UNC/network, device namespaces, alternate data streams and
unsupported filesystems with actionable errors. Keep private state out of shared
checkouts. Secure the directory atomically at creation with a protected DACL for
the owner; verify owner SID, effective rights, expected types and inherited grants
on existing directories/files. Database, lock, logs, snapshots, capabilities,
receipts and Caddy signing state inherit only that private boundary. Reject unsafe
existing state rather than repairing it. `chmod(0700/0600)` alone proves nothing
on Windows. Reject reparse points at private state boundaries and use retained
handles/revalidation to prevent path substitution during creation, open or cleanup.

Use an exclusive `LockFileEx` lock over a fixed byte range of the private lock file,
held from startup through listener/database closure. The lock handle is not
inherited. Never delete a live lock file; bbolt's own lock is additional protection,
not a substitute for serializing startup. Kernel handle release permits lock
reacquisition after crash. Named pipes disappear when handles close: there is no
Unix socket file to unlink. Under the lock, validate private generation metadata,
reconcile authenticated prior authority and only then publish a new generation.
Busy pipe, denied access, handshake timeout or mismatch means uncertainty, not
staleness. Doctor reports each separately without mutation.

Open checkout/project/Git directories and derive volume identity plus `FILE_ID_INFO`
file ID, retaining canonical final paths for display and move detection. Resolve
relative `.git` markers exactly as today; checkout and worktree Git directory IDs
both contribute, HEAD remains provenance. Compare actual handle identity for
case/short-name/junction aliases; never lowercase all paths or hash only path text.
Inspect per-directory case sensitivity. Recheck captured project/checkout identity
and canonical location before accepting snapshot intents. Aliases converge, distinct
worktrees differ, and moved/replaced checkouts fail without adopting old state.
Preserve ancestry protections against private-state mounts across junctions,
symlinks, case variants and hard links to files; deny ambiguous resolution.

## Windows owned runtime (#37)

Use one out-of-job guardian per workload with a retained **unnamed job object**.
Set kill-on-last-job-handle-close and disallow breakaway. Guardian is the sole job
handle owner; never inherit/duplicate that handle into the workload. Configure the
job and completion tracking before root creation. Use `CreateProcessW` with
`STARTUPINFOEX`, `EXTENDED_STARTUPINFO_PRESENT`, `CREATE_SUSPENDED` and
`PROC_THREAD_ATTRIBUTE_JOB_LIST` containing that retained job handle. Keep attribute
storage and the job handle live through the call; job assignment is distinct from
handle inheritance. Verify returned root membership through retained handles before
resuming its primary thread. Attribute setup, creation, membership or resume failure
aborts activation; terminate/wait any returned child through retained authority.
Never fall back to creating a root and then calling `AssignProcessToJobObject`.
Test nested jobs imposed by CI/agent launchers; incompatible nesting fails before
application activation, with no unsupervised fallback. Delegation to services,
WMI or external supervisors remains unsupported; job membership is not a hostile
workload security boundary.

Creation followed by assignment is **two operations**, even with a suspended root.
If the guardian crashes between them, an unassigned suspended child can survive:
parent exit does not terminate children, and kill-on-job-close covers only associated
processes. Microsoft's [process termination rules](https://learn.microsoft.com/en-us/windows/win32/procthread/terminating-a-process)
and [job lifetime rules](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)
support this inference; it is not Windows execution evidence. Microsoft documents
[creation-time job lists](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-updateprocthreadattribute)
from Windows 10/Server 2016 and the [extended CreateProcessW attributes](https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw).
This design removes the user-space create/assign interval; those API descriptions
alone do not prove every in-call guardian-crash or partial-failure outcome. #37 must
review those outcomes and demonstrate the startup fault gates below before this
adapter is accepted. If creation-time association cannot establish the boundary,
stop for a reviewed retained-authority design; do not ship the two-step fallback.
Any possibly created child lacking retained authority is a named residual, including
a possibly suspended unassigned root. Quarantine the launch journal/state, block
conflicting restart and report unresolved cleanup; do not infer absence from a
failed call, guardian exit or closed port, or recover authority from an observed PID.

Journal launch intent, create inactive guardian and private pipe, persist guardian
creation identity, random capability, generation and protocol version, then activate.
The guardian retains root process/thread handles, original exit status and collection
handles independently of descendants. Daemon crash must leave guardian authority
available for authenticated cleanup. Recovery challenges that exact generation;
a discovered PID, job name, executable or open port never authorizes termination.
Do not reopen a named job from journal text. A guardian crash closes its sole job
handle and requests kernel termination of associated processes only, but does **not**
itself prove all descendants and collectors joined or cover an unassigned root.
Missing authenticated proof remains uncertain, even if a port is closed. Preserve state, block conflicting restart and give an actionable
manual diagnosis; do not automatically resume/retry work or delete the journal.

For console commands, give the guardian an isolated console and launch the root
in its own console process group. Use targeted `CTRL_BREAK_EVENT` as the graceful
request; `CTRL_C_EVENT` cannot target an arbitrary process group. Retain the root
handle until after every group control event so its group ID cannot be reused;
never broadcast to the user's terminal. Commands must handle BREAK or tolerate
bounded forced shutdown. GUI/detached commands have no promised graceful console
handler; forced owned-job termination remains bounded and explicitly reported.
Validate console setup under native PowerShell, CI and the actual agent launcher
before adopting this adapter. An application-specific stop protocol is a later
explicit contract, not automatic shell scripting.

Retain existing startup/job/grace/lease semantics: defaults 5m startup/dependency,
30m job, 10s graceful stop; zero execution budget means unlimited, zero grace means
immediate escalation, and disposable leases remain finite (30s default; allowed
100ms–24h). After grace, `TerminateJobObject` acts only on the retained job handle.
Then query active-process accounting, wait retained root/guardian handles and join
collectors under separate finite deadlines; completion-port messages alone are
not final absence proof. Persist a generation-key-authenticated result/cleanup receipt, bound to workload
identity and operation, before reply; reject stale/replayed receipts for new work. Collection gaps, unjoined descendants or missing guardian exit are failures.
Original root exit, cancellation, infrastructure and cleanup results stay separate;
map Windows exit codes without truncating the stored unsigned 32-bit native value.
CLI exit mapping must preserve ordinary test exits and document exceptional Windows
status codes in #37; do not overwrite a real test failure with cleanup success.

CLI Ctrl-C sends typed cancellation and waits within the cleanup budget; it does
not signal daemon/children. Client loss stops lease renewal for disposable work;
expiry cancels it. Persistent accepted work remains daemon-owned. Ctrl-C on
foreground daemon serve initiates bounded daemon shutdown. Test second interrupt
and hung IPC without allowing an unbounded CLI wait; retained results remain
inspectable after reconnect. Stop preserves generations; restart is explicit;
reset/destroy keep existing conservative drift, ownership and data semantics.

Readiness requires both protocol success and owned endpoint evidence. Use
`GetExtendedTcpTable` owner-PID tables for IPv4 and IPv6 with exact address/port/
listener state, open process handles, verify creation identity and membership in
the retained job, and revalidate around the probe. Account for wildcard/dual-stack
conflicts and Caddy admin/HTTPS families. PID tables are observations only; they
never authorize cleanup. Access denied, vanished/reused process or ambiguous
membership fails closed. Exercise races with a foreign listener replacing an owned
one; a TCP/HTTP response alone cannot make that listener ready.

### Deterministic startup fault acceptance (#37)

Use private authenticated fixture barriers, disabled outside explicitly owned tests,
with a cleanup controller outside the faulted guardian. Before **each of the first
two faults below**, that controller must retain the guardian's direct launch handle and receive
and acknowledge a duplicated root process handle with termination/wait rights from
the creator over authenticated fixture IPC. Register bounded cleanup first; never
inject if the handle transfer fails. Retain thread authority where suspension must
be inspected, but never resume the residual to clean it. Do not duplicate the owned
job handle into the controller: that would defeat the last-close experiment.

- **After creation, before assignment:** a deliberately two-step, reproduction-only
  fixture creates a suspended root and blocks before `AssignProcessToJobObject`.
  After the controller acknowledges cleanup authority, crash the guardian. Record
  root survival through the retained handle, absence of application activation and
  the product's launch-failure/quarantine diagnosis. The job-close assumption must
  fail this regression. The controller then terminates/waits the residual through
  its retained handle; fixture disposal is not a successful product cleanup receipt.
  This hazardous sequence is never a shipping adapter or compatibility fallback.
- **After assignment, before resume:** the creation-time adapter returns a
  suspended root, verifies membership and transfers fixture cleanup authority, then
  blocks before resume. Crash the guardian after the controller's acknowledgement.
  Observe kernel-triggered root termination by waiting on the retained handle within
  a finite deadline, before any controller termination. Require no activation and
  preserved failed/uncertain product status without an authenticated completed receipt. If the root survives, fail the gate and dispose of it through
  the controller's retained handle; do not report kill-on-close success.
- **Creation-time boundary:** also gate before the create call (no root allocated),
  attribute/creation errors and membership/resume errors, and review guardian death
  during the call before returned handles can be transferred. The two barriers above
  do not prove this in-call case. Before any such destructive experiment, retain
  independent cleanup authority covering even a child whose handle was never returned
  (for example, an explicitly granted disposable VM with retained teardown control).
  An enclosing fixture job can mask the result and cannot prove the owned job's
  last-close behavior. If safe independent authority or crash/failure evidence is
  unavailable, record the case blocked and the association boundary unproven; keep
  uncertainty/quarantine semantics and return for review before accepting #37.

For every case record the exact fault barrier, handle-authority acknowledgement,
launcher/nested jobs, create/assignment/resume result, root/guardian waits, collector
joins, product diagnosis and fixture cleanup/residuals. Retained-root observation
and cleanup must remain separate from product recovery authority. Unrelated sentinel
processes survive. Timeouts or missing proof fail acceptance and preserve diagnostic
state; never reopen a PID from logs, job accounting or process enumeration to kill it.

## Windows commands, environment and paths (#37)

Commands remain executable plus argument arrays; no implicit `sh`, `cmd` or
PowerShell. Resolve native executables explicitly, retain absolute executable and
working directory, use Unicode CreateProcess semantics and a tested Windows
argument encoder. Test empty args, spaces, quotes, trailing backslashes, Unicode,
long paths and metacharacters. Do not join args into a shell string. `.ps1` requires
explicit `pwsh -NoProfile -File`; `.cmd`/`.bat` requires explicit `cmd.exe /d /s /c`
and documented cmd quoting owned by that declaration. Do not silently apply
PATHEXT shell dispatch. Probe commands obey the same rules as workloads.

Environment keys compare case-insensitively on Windows; reject conflicting
case-duplicate assignments before merge. Preserve explicit precedence, sensitive
redaction, daemon-start inherited values and required launch variables such as
SystemRoot. No runtime detection or trust injection. PATH uses Windows separators;
container PATH/targets remain Linux values. Treat host paths with `filepath` and
container targets with POSIX semantics. Reject drive-relative `C:foo`, ambiguous
root-relative paths and invalid/reserved names. Long-path support requires a real
fixture and recorded host policy, not only a prefix rewrite. Initially reject
unsupported forms explicitly; product expansion needs review. JSON/YAML examples
must quote Windows backslashes correctly (single-quoted YAML is simplest).

## Docker Desktop and managed HTTPS design (#38)

Accept an explicitly configured local named-pipe URL such as
`npipe:////./pipe/docker_engine`, confirmed with `docker context inspect`; the actual
Desktop Linux engine pipe may differ. Validate local namespace, no remote host,
credentials/query/fragment or TCP fallback. Never infer a context or change Docker
settings. Use official SDK transport with bounded connection/header/cancellation
behavior, Engine identity and Linux OSType checks. Report unavailable Desktop,
wrong engine and ACL denial separately. Native-only selection must make zero
Docker contacts; existing recorded private-directory destruction still verifies
configured Engine consumers and may fail when Engine is unavailable.

Pass absolute Windows host bind sources and absolute Linux targets as structured
Engine mounts, avoiding Git Bash/MSYS path conversion and colon-split `-v` parsing.
Verify source existence/type and host handle identity before create; inspect actual
Engine mounts, readonly flags and container-visible file bytes after create.
Desktop's VM source representation is backend/version dependent: do not reuse
Darwin `/host_mnt` rules, strip arbitrary prefixes or equate path strings by suffix.
#38 must capture actual representations and add a narrow verified translation
with host-identity/ancestry checks. Unknown mapping or foreign consumer inspection
blocks destructive directory cleanup. Cover drive/case/junction aliases, parent/
child/sibling boundaries, file hard links and source replacement races. Labels plus
recorded resource IDs/capabilities remain Docker authority; no global prune.

Mount only the existing public `root.crt` file read-only at a declared Linux file
target. Native `ca_cert_path` remains the Windows host path; container reference
resolves to its explicit Linux target. Never mount private parents, CA directory
or signing keys, copy certificates automatically or relax the private host DACL.
If Desktop cannot bind the single public file through the private boundary, fail
and return for an explicit design decision; do not silently introduce an export
folder. Demonstrate non-root read access without allowing parent traversal or keys.

Run stock native Caddy under the Windows guardian. Retain persistent private
CA/autosave/logs, pinned root identity, validated saved config and `--resume`,
`install_trust:false`, random owned scope/ETags, exact route cleanup and no bootstrap
replay on missing/corrupt state. Reject occupied ports; never take over a system
Caddy/service. Admin stays IPv4 loopback on a distinct unprivileged port (2020
default), with the existing trusted-user HTTP admin boundary; loopback is not
same-user authentication. CLI/guardian controls use private IPC. HTTPS defaults
IPv4/IPv6 loopback on 8443. No automatic widening, firewall edits or LAN admin.

Canonical origin remains `https://bl-<label>.backlot.localhost:8443` for host,
Edge, native and Linux consumers. Test ordinary Windows native/Go resolver and
Edge independently; browser `.localhost` behavior does not establish OS resolver
behavior. Containers explicitly map only the selected canonical hostname to
Engine `host-gateway` on each fresh create. Test actual Desktop forwarding to
host-loopback Caddy; Linux Engine/macOS evidence is insufficient. If it fails,
record refusal/DNS/routing evidence and review an explicit reachable HTTPS bind
using the existing setting. Do not silently widen, replace the canonical URL,
substitute `host.docker.internal`, change DNS/hosts, or introduce custom dialers.
If ordinary native canonical DNS needs operator preparation, document and prove
that prerequisite before claiming support; any new automation is a product decision.

Trust is separate for Windows system, Edge, daemon/native apps and containers.
The operator explicitly invokes stock `caddy trust --address 127.0.0.1:2020 --ca local`
against the verified owned gateway; record prompts, effective certificate store,
root fingerprint and scope. No assumption that it always targets current-user
rather than machine-wide trust. System/elevation actions require a fresh grant.
Edge honors locally installed roots subject to policy; verify the actual profile/
version and normal warning-free navigation. Restart daemon/clients if cached roots
require it. Go on Windows uses system roots by default; do not claim Unix
`SSL_CERT_FILE` changes Windows Go system trust. The native fixture can explicitly
load `ca_cert_path` into its application's root pool; other runtimes own documented
trust settings (Node can explicitly use `NODE_EXTRA_CA_CERTS`). Linux Go/Alpine uses
its declared readonly PEM and explicit `SSL_CERT_FILE`. All must reject missing/
wrong roots; no ignore flags, proceed-anyway, custom resolution or implicit env.

## Runnable example and acceptance sequence

#38 must ship `examples/windows-quickstart/` with source, locked inputs, machine
example and a PowerShell 7 README. It is a deliverable, not an existing runnable
example in #36. Reuse the generic Go HTTP/SSR/API/WebSocket fixture and Linux
consumer from existing examples, with explicit Windows command arrays; no Node
requirement for Backlot tooling. Include persistent dev and manual-update preview,
disposable integration success and an intentional exit-7 failure, retained reports,
Docker dependency private data and container SSR/test consumer. Application owns
initialization and application trust; Backlot owns lifecycle/resources.

The future README must supply literal copyable commands, exact tool/image setup,
normal canonical URLs, expected JSON/exit results and verified cleanup. Planned
PowerShell command shape below uses existing CLI flag placement, but **does not
run successfully on this baseline**. `C:\BacklotOwned` means a future granted
private fixture root, not a directory created or authorized by this design.

```powershell
$state = 'C:\BacklotOwned\daemon'
$machine = 'C:\BacklotOwned\machine.yaml'
$project = 'C:\BacklotOwned\repo\examples\windows-quickstart'
& .\bin\backlot.exe daemon start --state-dir $state --json
& .\bin\backlot.exe gateway start --state-dir $state --config $machine --json
# Separate operator-owned stock caddy trust and DNS/TLS verification gate here.
& .\bin\backlot.exe run dev --project $project --config $machine --state-dir $state --json
& .\bin\backlot.exe run integration --project $project --config $machine --state-dir $state --json
$testExit = $LASTEXITCODE # Record immediately, before logs/cleanup commands.
# Use returned instance IDs for status, logs, evidence, stop and destroy.
```

Run early vertical flows before broad coverage: #37 actual Windows CLI → private
daemon → native app, successful run, exit-7 and daemon-crash authenticated recovery;
#38 daemon → local Docker dependency/consumer → native Caddy canonical HTTPS,
correct/missing/wrong trust and occupied-port/unavailable-Engine recovery. Record
failures before fixes; a later success must name its new revision/configuration.

## Epic acceptance and evidence matrix

Every row requires real success **and** named negative/preservation evidence at the
frozen implementation revision. #36 supplies design/manual checks only; all Windows
execution rows are pending. Shared macOS/Linux regressions remain required.

| Epic #34 requirement | Owner / observable success | Failure, recovery and preservation gate |
| --- | --- | --- |
| Private API/state/locking/stale endpoints | #37: two clients, typed handshake, private DACL/owner inspection, one concurrent daemon winner | Other-user/remote denial with granted account fixture; unsafe inherited ACL, reparse swap, busy/foreign pipe, timeout, crash lock release; no deletion/repair |
| Checkout identity and concurrency | #37: alias convergence, two actual Git worktrees and concurrent scenes with distinct resources | Case-sensitive dirs, junction/short paths, moves/replacement during snapshot, missing Git directory; preserve original records/data |
| Supervision/listeners/readiness | #37: root/descendant tree, owned IPv4/IPv6 endpoints and command probes | Root exits early, child holds output, nested-job refusal, [startup faults before assignment and before resume](#deterministic-startup-fault-acceptance-37), creation-time uncertainty, PID reuse/replacement, unrelated listener, denied table access; no foreign termination |
| Lifecycle/leases/results/evidence | #37: idempotent dev, disposable success/exit-7, original result/log/artifact access, stop/start/restart/reset/destroy | Ctrl-C, second interrupt, client loss/lease expiry, hung probes, grace expiry, collector failure, daemon/guardian crash, wrong/replayed capability; uncertainty retained |
| Windows argv/env/paths | #37: native exe and explicit pwsh, Unicode/space/empty/quote/backslash args, env precedence | Shell metacharacters remain args, case-duplicate env rejected, drive-relative/unsupported paths diagnosed, missing executable and replaced snapshot source rejected |
| Docker dependencies/consumers | #38: actual local npipe, Linux Engine, private network/volume/directory, published ports, inspected mounts | Denied/missing/wrong engine, Engine restart, create intent/effect crash, foreign consumers, alias overlap, readonly/non-root root mount; host-only zero contacts; unrelated resources preserved |
| Managed CA/private admin | #38: stock Caddy, stable root/origin across stop/start, exact route cleanup, admin loopback | Occupied ports, bad autosave/keys/root, wrong scope/ETag, gateway/daemon crash and authenticated cleanup; preserve peers/CA/data, no fallback |
| Canonical HTTPS and application behavior | #38: ordinary host/Edge/native/container DNS+verified TLS, frontend/API, SSR, variable text/binary WebSockets, redirects, secure cookies | Missing/wrong trust per consumer, blocked DNS/routing, unreachable upstream, foreign listener, scoped-route removal; no bypass or URL substitution |
| Real CI, example and adoption | #37 native runtime CI; #38 provider CI plus Windows 11 Edge/system walkthrough and four example workflows | Fresh install, two worktrees, failed test retained after teardown, interrupted run/recovery, unrelated sentinel process/port/file/container survives; absent prerequisites fail configured gates |
| Documentation/release boundary | #37 partial status; #38 README/platform/operations/HTTPS/testing/architecture/tracking updated to evidence | #25 binaries only after verified target; exact published-HEAD CI and independent reviews; skipped/blocked gates prevent epic closure |

## CI, desktop evidence and implementation handoff

#37 adds a real `windows-2025` x64 GitHub Actions job with `pwsh`, pinned Go from
`go.mod`, native Git, native CLI build and owned runtime/ACL/identity/lifecycle
fixtures. Windows Server runner results are explicitly Server results, not Windows
11 desktop evidence. Pin/check runner image/tool versions; run race checks with
an explicitly provisioned compatible C compiler or report that gate blocked.
Invoke Go tooling directly in PowerShell (ADR verify/test, schema/notices checks,
vet, build and applicable native tests); do not assume Make or Unix test fixtures
work. Port test adapters without silently build-tagging away lifecycle coverage.
Retain existing macOS/Ubuntu workflow jobs and their provider/browser gates.

#38 adds real Windows 11 x64 provider acceptance on a dedicated disposable
self-hosted runner or explicitly granted desktop session with actual Desktop Linux
containers and stock Caddy. Ordinary hosted Windows CI must not be assumed to
supply Desktop Linux virtualization or an interactive Edge profile. Provisioning,
runner credentials and VM/backend setup are operator work with separate grants.
Run provider jobs serially with unique owned state/resources and no unrelated
pull-request code on a personal persistent runner. Missing configured tools,
Engine or trust fails; unconfigured opt-in skips are reported incomplete, not green
acceptance. Require this job/result for the exact candidate and repeat on final
published HEAD; if unavailable, readiness is blocked rather than replaced by a
cross-build. Keep interactive adoption separately recorded even if CI passes.

Actual Windows 11 desktop adoption starts from a recorded baseline of root stores,
Edge profile/policies and normal resolver configuration. With explicit trust/resource
grants, demonstrate missing trust failure, stock Caddy trust installation, matching
public root identity, normal warning-free Edge navigation and native daemon readiness,
then native/container trust negatives. Use clean owned profiles or isolated VM
snapshots so a prior root cannot counterfeit success. Remove only the root installed
by this walkthrough with exact fingerprint and approved store scope; never reset
the user's stores. Retain private CA state when resources remain; do not uninstall
trust while other accepted scenes depend on it.

Implement the accepted ADRs and stop for consequential decisions if the native
resolver, required console behavior, single-file private mount or listener
reachability cannot satisfy this design. #37/#38 must establish exact
API/state migration and Windows CLI exit mappings with contract tests before
shipping. No persisted Unix state is automatically adopted as Windows state.

Each run's external evidence records commit and dirty patch (if any), command,
execution OS/shell/agent-launch constraints, fixture/config/image digest provenance,
versions/backend, result/exit code, logs/reports, failures/skips, timing when known,
resource grant and cleanup/residuals. Store capabilities, raw secrets and private
state outside public reports. Register cleanup authority before injecting faults;
act only through retained handles/authenticated controls or owned Engine IDs.
Never kill by task name/PID/port, prune globally, change shared services/trust/DNS,
or delete uncertain state. A closed port or successful mock is insufficient.

#36 checks ADR verification/tooling tests, repository links/command shapes,
accepted-contract consistency and docs-only diff. Independent Low and fresh High
reviews, fixes, frozen local checks and final published-HEAD repository CI gate its
readiness. Runtime/provider/adoption checks are intentionally unexecuted in #36.
