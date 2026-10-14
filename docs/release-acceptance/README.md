# Release verification

Use this guide to prepare and review release evidence. It describes maintained
requirements, not the outcome of a particular run or a release declaration.

## Required gates

Freeze the candidate revision after independent standard and sensitive reviews
and any required fix verification. Run the complete gates against that candidate:

- Repository checks, schema/ADR validation, race tests and build (`make check`).
- Complete Docker runtime and disposable evidence integration suites.
- Gateway fixture contracts, complete native/container gateway suite and HTTPS
  runtime suite, run serially with exclusive fixture mutation ownership.
- Public four-workflow acceptance using the generic application, real independent
  worktrees and approved operator DNS/TLS: native development, explicit-update
  container preview, selected database tests and production-like browser tests.
- Real trusted host browser and host/container HTTPS checks, including SSR/API,
  variable-payload WebSockets, redirects and cookies. Fixture dialing overrides
  cannot establish operator DNS/trust acceptance.
- Reproducible macOS ARM64 release builds from independent source trees/caches,
  linked dependency notices verification, and public workflows with the produced
  release binary. Check module metadata and supported compilation boundaries.
- Ownership-scoped cleanup and preservation of unrelated operator resources.
- Existing required CI success on the exact published HEAD before readiness.

[Testing and tooling](../testing.md) owns the test commands, opt-in settings and
skip semantics. [Runnable fixture setup](../../examples/release-acceptance/README.md)
owns application/image builds and workflow commands. Use [operations](../operations.md),
[HTTPS setup](../https.md) and [evidence retention](../evidence.md) for operator
preparation, publication ownership and retained runtime results. Do not duplicate
those command lists here. An absent opt-in setting or skipped suite is not proof
of acceptance; provider-independent checks do not replace the required integrations.

## Evidence expectations

Record exact source revision and any dirty patch, command/environment selections,
exit status, failures/skips, timings and case coverage. Bind results to source,
toolchain, release binary, image IDs/digests, fixture and private configuration
hashes. Record build cache use and distinguish setup time from workflow time.
Retain review dispositions, CI URLs, cleanup receipts and uncertain residuals.
Never infer a pass from an earlier revision or relabel a focused check as a complete
final cycle. Source changes require reassessing affected gates; a docs-only
carry-forward must prove unchanged runtime/test/fixture/build/dependency inputs
and identify the original execution revision separately from current CI HEAD.

Use private owned state and register cleanup before faults. Preserve uncertain
ownership for diagnosis rather than deleting it. Sanitize published evidence;
exclude secrets, raw private configuration/state and ownership capabilities.

## Supported platform boundary

The release runtime target is macOS ARM64. Linux checks are portability evidence;
Windows cross-builds establish compilation only. Neither proves runtime lifecycle,
networking, installation or supported release behavior on those platforms.
Mocked checks and fixture-only DNS/TLS do not establish real operator acceptance.

## External retention

Keep run-specific case matrices, screenshots, logs, intermediate provenance,
review handovers and delivery accounting outside the source tree. Retain a private
revision-bound archive with a SHA-256 manifest before removing committed evidence.
Publish sanitized summaries and evidence links in the relevant PR discussion or
approved artifact storage, and state which artifacts remain private or unavailable.
The PR synopsis should identify current HEAD/CI and link the original execution
provenance; preserve historical discussion and shared Git history. Retention is
an explicit delivery responsibility: do not rely on temporary test directories or
expiring CI artifacts as the sole evidence copy.
