# Targeted runtime diagnostics and evidence

[Preparing a release](../preparing-a-release.md) owns release readiness: reviewed
notes and applicable required CI at the exact reviewed revision to be published,
followed by fresh tag-workflow checks of the final archives. Development/PR
acceptance and routine releases do not require a duplicate local full-provider,
SSH, system-trust or four-workflow cycle, or a separate V1 qualification.

The owner retired the original manual MVP adoption gate. This is a policy change,
not evidence that earlier unexecuted checks passed or historical failures were
resolved. V1 named the initial product scope; semver `1.0.0` remains a deliberate
stability commitment, and current release automation accepts only stable `v0.x.y`.

## Selecting additional checks

Select manual tests when a change introduces risk to a product guarantee without
CI coverage. Use the smallest relevant maintained fixture or operator check:

- [Testing and tooling](../testing.md) lists provider, gateway, native/container,
  browser, installation and recovery suites with opt-in settings and skip semantics.
- The [runnable four-workflow fixture](../../examples/release-acceptance/README.md)
  exercises native development, explicit-update container preview, database tests
  and production-like browser tests with independent worktrees.
- [HTTPS setup](../https.md), [operations](../operations.md) and
  [platform policy](../platforms.md) describe real DNS, trust and platform boundaries.

Existing isolation, cleanup, HTTPS, recovery and truthful-result guarantees remain
in force. CI and selected fixtures establish only what they exercise: compilation
is not runtime evidence, skipped tests are not passes, and fixture DNS/trust cannot
prove operator/system/browser trust. A targeted check does not imply a complete
regression cycle. No routine repeat of unrelated manual suites is required.

## Safe execution and useful evidence

Use private owned state, register cleanup before faults, and run fixtures that
mutate the same providers serially with exclusive fixture ownership. Preserve
uncertain resources for diagnosis; never delete unrelated operator resources or
use TLS bypasses to turn a failed check into success.

For selected checks, retain the tested source revision/dirty patch, command,
relevant tool/config/image inputs, results including failures/skips, and cleanup
or uncertain residuals. Sanitize shared evidence; exclude secrets, private state
and ownership capabilities. Keep run-specific logs, screenshots and delivery
accounting outside the source tree, with durable links where useful. Earlier
results retain their original revision and limits; a policy change or later CI
run does not rewrite their provenance.

The four-workflow fixture targets macOS ARM64. Ubuntu CI and Debian actual-host
results remain distinct, with bounded Linux browser trust reported separately.
Neither establishes WSL UX or native Windows support. Select new manual platform
checks for uncovered change risks; a target listed in policy does not prove that
an untested release passed there.
