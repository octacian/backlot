# Release acceptance ledger — issue #8

Run `run_85d06ab1b7e9`; initial base `00d8ed85d974103f95d8bb631ea11e2fed7a612e`;
branch `feat/release-acceptance`. Source, fixture cleanup and eventual PR owner:
implementation worker; coordinator owns routing/approvals and read-only review
handoffs. No branch/PR publication at the first candidate boundary.

## Approved brief and gates

Implement milestone 7 and every PRD release acceptance case with self-contained
generic fixtures on macOS ARM64. Include native development, explicit-update
container preview, disposable database integration and production-like browser
integration; verified setup/CLI/manifest/recovery, reproducible binary/notices,
actual cold/warm startup/adoption measurements. External migration and CI
orchestration/parity are excluded. Linux/Windows compilation is architecture
portability evidence only. Do not change semantics or weaken assertions silently.

Initial scope **5**, complexity **4**, size index **20** (`scope-complexity/v1`):
cross-cutting release workflows; interacting concurrency/recovery/provider
constraints. No sizing revisions. Effective implementer requested by dispatch:
Codex `gpt-6.1-sol`, medium. Actual usage/cost unavailable (`null`). Independent
low review then fresh high review, serial checkout ownership, and final frozen
validation are required. First candidate must pass focused checks and `make check`
before Conventional Commit. Final frozen validation must repeat complete `make
check`, `TestDockerRuntime`, `TestDisposableEvidenceDocker`, gateway fixture tests,
full opt-in gateway suite, `TestHTTPSRuntime`, all public acceptance workflows,
real trusted host/browser/container HTTPS and reproducibility/license verification.
Published revision CI must pass before readiness. No merge/tag/deploy/release.

## Resource and secret boundary

Exclusive mutation grant covers uniquely owned fixture daemons/private state,
dynamic ports, labeled Docker resources/images and fixture Git worktrees.
Operator gateway mutation is restricted to exact ownership-marked children in
existing `backlot` subroute at local admin port 2019. No whole-config load,
restart, DNS, certificate, key or trust changes. Existing wildcard domain grant
is used only by fixture routes. Temporary machine config/private journals remain
outside checkout. No secrets, lease tokens or ownership capabilities in evidence.
Preserve and report uncertain residuals; never infer ownership from name/PID.

## Checkpoints

- 2026-10-09T18:21:40Z: clean baseline verified, mandatory guidance read, issue #8
  fetched. Host `Darwin arm64`, Go `go1.27.1 darwin/arm64`. Docker context
  `desktop-linux` local socket reachable; gateway scope read as empty explicit
  routes array. BrowserOS neo connected. Existing fullstack example references
  absent commands and is not a runnable adoption workflow.
- Implementation in progress: new `examples/release-acceptance` application uses
  Go/MySQL driver with MariaDB image, browser-rendered JavaScript, SSR over the
  canonical origin, variable WebSocket echo, redirects/cookies. Application owns
  SQL/preparation/build/test behavior; Backlot public interfaces own lifecycle.

## Results and pending acceptance

Early real-flow and multiple public workflow checks passed (details below).
No criterion has final frozen-candidate acceptance yet. Reviews, frozen final
validation and CI have not run. Record exact command/revision/result/artifact evidence here as gates run;
unit/fault-injection tests supplement and never replace public workflow evidence.

## Findings

- F1 (`test_harness`, closed): runnable generic fixture added and public workflows
  exercised; the older fullstack example remains illustrative.

## Cleanup and ownership handoff

Acceptance runtime has been exercised. Public suite fixture daemons/instances
were destroyed through public commands and their private state/evidence retained.
The early manual daemon/dev instance and owned image tags were explicitly
cleaned before handoff (details below). Shared pre-pulled images remain operator cache. Two uncertain
legacy browser profile directories are preserved below. The exact candidate commit
is recorded in the coordinator handoff (a commit cannot embed its own hash).

## Early real-flow evidence (dirty application fixture against base)

Commands: `make build`; `go build -o examples/release-acceptance/fixture
./examples/release-acceptance`; static Linux ARM64 fixture build; explicit
`docker build --label backlot.acceptance.run=run_85d06ab1b7e9 -t
backlot-release-run85:acceptance examples/release-acceptance`; private machine
config, `backlot daemon start --lease-duration 3s`, `backlot run dev --json`.
Private root `/tmp/bl-release-_emhm6wy` holds command JSON/logs and private state;
these raw files are **not publication artifacts**. Sanitized facts follow.

- Dev `f362087e7cf8955c739d50d9caf121f3013089d8e74c97549bc15678a1cbd996`
  ready; published origin
  `https://bl-f362087e7cf8955c739d50d9caf121f3013089d8.dev.thatoneguy.ca`.
  Longest path `/api` strips prefix to native API; `/` preserves to native web.
  Both are child effects of configured `backlot` scope; TLS uses operator trust.
- BrowserOS neo loaded the real origin with normal DNS/TLS, body acceptance
  marker appeared only after HTTP API read and random-payload WebSocket echo.
  Rendered SSR read `fresh`; Save value button changed API data to
  `browser-mutated`. No certificate override or insecure browser flag.
- `run backend-test -- -run TestDatabase --fail` returned nonzero, status failed,
  original terminal exit 1; collection/cleanup fields empty. Declared result
  copied to `state/artifacts/<instance>/<attempt>/backend-test/result` before
  disposable teardown. Report JSON is `{test, arguments, passed}`.
- A fresh `run backend-test -- -run TestDatabase` succeeded, terminal exit 0,
  independently reading `fresh` then verifying its own mutation. Persistent dev
  data remained separate. Logs are typed JSON records (instance/component/UTC
  time/stream/attempt/message) through `logs`, not application stdout on run.
- Actual failed discovery attempt: `backlot list --state-dir ... --json` rejected
  the flag because list was absent. Public JSON inspection also exposed a private
  snapshot allocation token; no token is copied into this ledger.

F2/F3 are closed by named bounded discovery/status and response-boundary
redaction. Focused public transport tests verify pagination, ordinary redaction
and retained private recovery authority. New clients reject token-bearing views;
use matching CLI/daemon binaries after an explicit daemon stop.

## Candidate implementation checkpoint (2026-10-09)

Runnable fixture sources: `examples/release-acceptance`; public CLI suite:
`internal/acceptance`. All four workflows ran from the documented explicit setup.
Browser tests use an optional Chromium container with Chromedp waiting for actual
DOM state, never a no-op selector or fixed echo. `-run` is a regular expression
and unmatched selectors fail. Current browser test clicks Save, waits for API
mutation, reloads and verifies SSR persistence; the interaction extension passed its focused browser run and retained a parsed
DOM and actual PNG screenshot before cleanup. Generic fixture
application owns SQL/preparation/build/test behavior, while Backlot owns lifecycle.

Public discovery uses `POST /v1/instances` with named request/response and pages
of at most 100 summaries; `status` aliases instance inspection. Snapshot tokens
remain in private persisted state and are copied/redacted at the single HTTP
instance-response boundary. Required-service diagnostics now distinguish unknown
supervision/inspection failures from verified application exits, without accepting
uncertain runtime as healthy.

### Commands, source and outcomes

All logs below are private at `/tmp/bl-release-_emhm6wy`; publish only these
sanitized summaries and checked artifacts. The source base is the commit above.
Patch hashes are SHA-256 of the complete staged binary diff against that base;
patch files retain source provenance locally. Early exploratory source did not
have a complete untracked-file patch snapshot; do not attribute it to a later
patch. Initial raw JSON is private and may contain the pre-fix token/initial
lease capabilities. Never publish raw state or those responses.

| Attempt | Provenance | Observed outcome |
| --- | --- | --- |
| Early manual dev/browser/backend | Base plus exploratory fixture; exact complete dirty patch unavailable | Trusted BrowserOS SSR/API/variable WebSocket passed; Save mutated private DB. Failed backend exit 1/report followed by fresh selected exit 0. |
| Initial native Chrome UI | Original private state, normal host Chrome with private profile | Hung; bounded timeout/explicit daemon stop joined owned groups; terminal status -1 and missing browser artifact correctly caused failure. No success claim. |
| Container browser first attempt | Explicit Chromium image setup | Image not yet available at invocation; Docker create failed. Harness ordering error, not missing-provider success. |
| Container browser dump-DOM | Built image with Chrome CLI dump-DOM | One failed DOM-readiness assertion and one subsequent pass; timing race identified, replaced by state-based Chromedp checks. |
| `public-workflows-1.log` | Patch `a789cc9f699de28ecc14ed4ee8c9e1f9fd78dcdc0b60010867ec779fe651c06a` | PASS, 58.90s: two real worktrees, duplicate starts, coexistence, isolated selected DB tests, kept failure/logs after destroy, retained data/reset, explicit preview update, real container browser, shutdown/crash recovery. |
| `public-workflows-2.log` | Patch `fe87050e4a70c1974a49cca041d9e4a4bc311d7f3a9cfbb0d4f7f72a2e43abba` | FAIL, 67.12s: artifact timing assertion read a directory as a file; bad probe was rejected before execution. Secret boundary, SIGINT/client loss and browser passed. |
| `public-workflows-3.log` | Patch `e5bede49e099fc0425787f61caee735f2990f952166fffb533f2a9b232febf30` | FAIL, 74.71s: serialization timing now passed; nonpublished bad-probe scene still selected an origin and was correctly rejected. Other expanded cases passed. |
| `probe-focused.log` | Patch 3 plus diagnostic assertion | FAIL: clarified exact planner code `invalid_publication`; removed unused selected origin from the failed-probe scene, preserving runtime-failure assertion. |
| Focused named contract checks | `focused-contracts-2.log`, `focused-contracts-3.log` | PASS: client/CLI/daemon discovery, response redaction/private authority and owned metadata crash recovery. Initial contract run failed on old token-bearing test response; fixture updated to actual redacted contract. |
| `make-check-1.log` | Candidate before lint fixes | FAIL at lint: deprecated GOROOT lookup, HTTP numeric constants and boolean-expression style; corrected without changing runtime assertions. |
| `make-check-2.log` | Candidate before final small CLI/client polish | PASS, including daemon race suite 265.436s, all normal checks, ADR verification and build. |
| `browser-probe-focused-2.log` | Browser interaction + corrected runtime probe | Browser and preparation/probe subtests passed; overall FAIL on intentional missing artifact from never-run terminal job causing truthful daemon stop nonzero. |
| `browser-probe-focused-3.log` | Corrected isolated preparation fixture | PASS, 36.64s; browser UI mutation/reload and actual failed preparation/probe; clean public destroy and daemon shutdown. |
| `make-check-3.log` | Follow-up source check | PASS; final small CLI/client polish and stronger private-authority assertion receive final candidate checks below. |

These are implementation/focused validation cycles, **not** post-review full
final-validation attempts (observed final attempts: 0). Review passes completed:
standard initial 0, standard fix verification 0; planned independent low then
fresh high have not started. Sensitive gate requirement is coordinator-owned and
not inferred here. Published CI state: unavailable (no PR/branch publication).

### Resource/config/image provenance

Private machine config: `/tmp/bl-release-_emhm6wy/machine.json`, mode 0600. It
uses local `desktop-linux` Docker socket, container native-host address
`host.docker.internal`, loopback mapped binds; native Caddy upstream loopback,
existing `backlot` scope and approved real wildcard domain. No certificate key,
capability or machine input values are copied into this ledger. Config hash is
recorded below; fixture images are explicitly built/pulled, never implicitly
pulled by Backlot. Operator gateway/DNS/trust setup remains unchanged.

Shared dependencies: `mariadb:11.4` resolved digest
`sha256:1292844148b311e4ed4300022a996d39083f415a963e970cf47cad1b3b18e3a6`;
`alpine:3.21` digest
`sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507`.
Browser image explicitly installs Alpine Chromium `136.0.7103.113-r0`; Go source
pins Chromedp `v0.13.7`, MySQL driver `v1.9.3` and WebSocket `v1.8.15`. All linked
module license/notice texts are generated from local pinned sources, including
Go standard library, in `THIRD_PARTY_NOTICES.md`. Images contain public CA roots;
no insecure TLS flags or explicit DNS dialing substitutions were used.

Public suite roots: `/tmp/bl-public-4053297558` (pass),
`/tmp/bl-public-1825151388` (failed harness), `/tmp/bl-public-1975179284` (failed
probe harness). Private state/artifact trees remain as evidence; suite cleanup
reported successful public destroy/daemon stop. Later roots are recorded by their
logs. Temporary fixture Git repos/worktrees live entirely inside their root and
have no remote; no repository-managed checkout was changed by them.

### Browser evidence and cleanup

Committed sanitized artifacts: `artifacts/browser-container.jpg` (initial actual
JPEG from patch-1 run, visually inspected), `artifacts/browser-container.png`
(current actual PNG proving UI mutation/reloaded SSR, visually inspected),
`artifacts/browser-report.json` (parsed DOM proof, explicit missing exact dirty
image-source provenance), `artifacts/browser-host.png` (BrowserOS early page
screenshot). Initial image-source snapshots were not fully archived at build;
final frozen-source image rebuild/acceptance remains required rather than claiming
that current exploratory screenshot proves a frozen candidate.
Container screenshot shows real SSR/API/WebSocket state. BrowserOS early page
performed Save through the UI; its screenshot is a later capture of that loaded
page, **not** a frozen-source rerun. Current screenshot code requests true PNG.
Ordinary app fixture values in screenshots are synthetic, not credentials.

Legacy native browser attempts returned no Backlot group cleanup failure. A
read-only process inventory restricted to Chrome/Chromium/crashpad executable
names found **zero** consumers with fixture `backlot-browser-*` profile args.
An initial broad text match counted the checking shell itself and was corrected;
no observed PID was signaled. These two unmarked temporary profile directories
are preserved because prefix/name alone does not prove cleanup authority:

- `/var/folders/gl/0k7lh75d1kj1mq_0v9xrk6xm0000gn/T/backlot-browser-916996`
- `/var/folders/gl/0k7lh75d1kj1mq_0v9xrk6xm0000gn/T/backlot-browser-684578188`

The final browser fixture is containerized; Docker owns all browser descendants.
Neither host profile cleanup uncertainty nor an observed absence is silently
converted into a stronger kernel-containment claim.

### Findings and uncertainty

| ID | Category | State | Evidence/action |
| --- | --- | --- | --- |
| F1 | test_harness | closed | Added a runnable generic application; earlier fullstack example was illustrative. |
| F2 | product | closed | Missing list/status; bounded typed discovery and status alias pass focused public transport checks. |
| F3 | product | closed | Snapshot token exposed by ordinary response; redacted all HTTP instance responses, private authority preserved and tested. |
| F4 | unknown | open, release-blocking | Initial manual dev and preview DB services stopped with normal DB exits, old diagnostic only said required service exited. Primary inspection/log error was discarded; Docker event ring no longer covers original time. Current diagnostics preserve distinct cause. Subsequent clean coexistence/recovery passed; early dev explicit restart stayed ready over ten minutes. Original cause is not proven by those passes. |
| F5 | test_harness | closed | Host Chrome dump-DOM hang/timing; container Chromedp waits for DOM and collects evidence. Legacy unmarked profiles preserved above. |
| F6 | test_harness | closed | Test image invocation raced image setup; explicit setup ordering corrected. |
| F7 | test_harness | closed | Artifact collection returns a directory; timing report read corrected to `result/result.json`; real intervals demonstrate shared output serialization. |
| F8 | test_harness | closed | Failed-probe fixture selected origin without publication; selected resource list corrected; actual runtime probe failure passed. |

Corrective human interventions observed: 0; routine coordinator diagnostic pointers
and planned gate reminders are not counted as corrective human interventions.
Human minutes/usage/cost are unknown (`null`). No sizing revision.

### Measurements (observations, no targets)

One public-suite sample on macOS ARM64, prebuilt images/toolchain cache warm:
first dev scene startup 5,464ms; stopped retained dev startup 2,742ms; ready
idempotent run 28/29ms; initial preview 3,958ms; second worktree dev 3,468ms;
initial production UI run including fresh DB/container/browser/evidence/cleanup
7,888ms. These are different lifecycle states, not benchmark claims. Here
"cold" means fresh Backlot data/resources, not uncached toolchain/image/network
setup. Record sample count 1 and no performance threshold. Configuration size
counts follow; source code is application preparation/testing, not copied
Backlot ownership/port/cleanup glue.

### Remaining gates and carry-forward scope

Before first candidate: finish current focused browser/probe checks, `make check`,
reproducible-build comparison/notices verification, cleanup owned manual daemon
and images, commit reviewable candidate. Carry-forward to the mandated post-review
frozen cycle: complete `make check`, complete `TestDockerRuntime` and
`TestDisposableEvidenceDocker`, gateway fixture and full opt-in suite,
`TestHTTPSRuntime`, complete public acceptance including trusted host/browser/
container evidence, final reproducibility/notices verification and cleanup.
Review both independent passes before freezing. All missing gates remain
incomplete. F4 requires diagnosis/resolution before readiness; no release claim. Exact
frozen-source image build provenance must also be completed after reviews.

Public cases still requiring additional workflow evidence/final verification:
unexpected service exit, deterministic port-conflict preservation, unrelated
Docker/Caddy sentinel preservation, uncertain ownership, injected collection/
cleanup failures and live log-follow framing. Existing owned fault-injection
suites supplement those cases but do not replace public-interface evidence.
Final PR closes #8, preserves concise readiness/evidence synopsis, includes final
collapsed `feature-efficiency/v1`, and requires published-revision CI success.
No merge/tag/deploy/release publication is authorized.

Current private config SHA-256: `63dc7279a478fb4110ca55f748bf95d7951ea1b2778b60b6c1b118829fd48d22`.
Fixture `backlot.json`: 839 lines, 16546 bytes.
Fixture `machine.example.yaml`: 14 lines, 433 bytes.
Fixture `main.go`: 353 lines, 13150 bytes.
Optional static seed files: 0. These counts are observations, not adoption targets.

## Pre-review cleanup and local build checkpoint

All nine manual-state records were explicitly destroyed by ID (all returned
`destroyed`, exit 0). Manual daemon stop returned exit 1 for retained historical
missing browser artifacts, consistent with its contract; its socket was then
verified absent. Public focused suite's final destroy/daemon stop passed. Gateway
scope child count verified **0** after all acceptance daemons were stopped. No
whole-gateway changes or unrelated runtime actuation occurred.

Exactly owned image labels `backlot.acceptance.run=run_85d06ab1b7e9` were verified
before removal, and each had zero consumers:

- Application: `sha256:4c97edc991b01fe009b7af6aa33078ca933e3a6e5ce355f65687cd88b3233eda`.
- Browser: `sha256:f0be1695e07ea48176cbbb0fa37c6a9c74ffd89d31d72a861b5bd0da7e77e425`.

Both images removed with `--no-prune`; shared base images/build cache preserved.
Six owned temporary fixture Git repositories, each with two worktrees, removed
through verified Git topology. Initial path comparison conservatively preserved
them because macOS canonicalized `/tmp` to `/private/tmp`; after verifying that
alias, exact owned worktrees/repositories/binaries were removed. Roots retain
private state/artifacts/logs only. Generated binaries/dist/reports in the new
checkout fixture directory were removed. No provider runtime residual is known;
the two unmarked browser profile directories above remain uncertain/preserved.

Two local macOS ARM64 binaries built from separate extracted staged source trees
and independent compiler caches matched byte-for-byte. Staged tree at that
comparison: `b3dce84edc129347da118e61aad5c83c72e577cf`; binary SHA-256
`b9436fa10c554770353b4ca4174704dca7fdbea36e8652394d347dbb353f9016`.
The refreshed comparison below includes final human URL output and strict
list-origin validation. Notice verification passed in both independent builds.
Required post-review reproducibility will repeat on the frozen candidate.

Additional harness correction: a readiness-only failure scene no longer declares
an artifact for its never-launched terminal job. The missing artifact correctly
made shutdown fail; shutdown semantics were retained, and separate collection
failure acceptance remains a required case. Application test startup now replaces
prior report output with a truthful `not_completed` result before validating
selectors, so cancellation/invalid selectors cannot retain a prior passed report.

## Final candidate preparation (before independent reviews)

Final source polish prints the canonical URL in human instance output and applies
the same canonical-origin validation to list summaries as execution responses.
The crash regression again checks exact private snapshot token stability across
recovery, reading only while its retained owned daemon is stopped; public
redaction does not weaken that private ownership assertion. Focused private
crash/discovery race checks passed (`focused-private-authority.log`), as did CLI/
client checks (`focused-final-cli.log`) and lint (`final-lint.log`).

Final code snapshot tree for independent local builds:
`01df50dcd501d30261df48a1435fd246390b626f`. Separate source directories and
independent compiler caches produced identical binaries, SHA-256
`5e7bc3019d73987bcfb9503ca2e7f53852fc0a311346fd27d8b9cb8b4f628a5e`.
The later private-authority test-only change does not enter the shipped binary.
`artifacts/build-metadata.txt` now records the final binary's toolchain/module/
flag metadata. `repro-final-one.log`/`repro-final-two.log` retain notices-check and
exact build command success. Exact source-candidate `make check` passed
(`make-check-exact-candidate.log`): schema/notices/format/vet/lint, complete default
race suite, ADR verification and build. Opt-in runtime tests remain separate;
post-review frozen acceptance is still unexecuted.

Additional distinct findings, all closed before candidate:

| ID | Category | State | Resolution |
| --- | --- | --- | --- |
| F9 | test_harness | closed | Isolated preparation fixture no longer requires evidence from an unlaunched terminal; retained collection/shutdown semantics unchanged. |
| F10 | product | closed | List origin lacked semantic client validation; now shares execution-origin validator. |
| F11 | product | closed | Human instance output omitted published URL; now prints canonical URL. |
| F12 | test_harness | closed | Initial screenshot was JPEG with PNG filename; current collection requests true PNG and prior JPEG is accurately named. |
| F13 | test_harness | closed | Early selector/cancellation could leave prior report output; startup replaces it with truthful incomplete result. |
| F14 | test_harness | closed | Public token removal prevented former public token stability assertion; private stopped-daemon reads restore exact recovery stability check. |
| F15 | test_harness | closed | Initial lint gate rejected deprecated GOROOT lookup and Go style in new tooling/fixtures; go-env discovery and idiomatic forms pass lint. |

Known distinct findings: product 4 (closed), test_harness 10 (closed),
environment_resource 0, unknown 1 (F4 open). No completed independent review
passes yet. Full final-validation attempts remain 0; multiple implementation
checks above must not be counted as frozen complete acceptance attempts.

Final checkout release binary matches both independent build outputs with the
SHA-256 above. Git whitespace inspection reports one inherited trailing space
in a verbatim third-party license; the notices generator deliberately preserves
upstream legal text rather than rewriting it. Build metadata display whitespace
is normalized in the sanitized artifact.
