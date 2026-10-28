# Contribution workflow

Inspect the affected package and its nearest analogue before introducing code.
Use a reference named by the requester first. Explain meaningful differences or
conflicting patterns before choosing a direction. Consult relevant accepted ADRs
for architectural or cross-cutting changes.

Keep changes focused and reviewable. Group code by domain and responsibility,
reuse established helpers, and introduce abstractions only when they remove
real duplication or clarify an existing boundary. Avoid speculative frameworks,
generic utility packages, and unrelated refactoring.

Use established libraries when they solve a requirement with less maintained
code. Evaluate API fit, maintenance, license, supported platforms, and dependency
cost. Prefer the standard library when it already provides the needed behavior.
Keep integration adapters thin; do not reproduce an engine's client, parser, or
storage implementation. Add dependencies when used, not in anticipation of work.

Plan meaningful work in reviewable steps and name affected packages. Exercise
failure behavior as well as success. Report the resulting behavior, validation,
remaining work, and material uncertainty. Do not present scaffold commands or
mocked integration checks as a working runtime.

Document a rule once in its relevant guide. Keep ADR rationale in the ADR and
reference it elsewhere. Treat generated files as outputs: change the source and
regenerate, review the diff, and document generation commands when introduced.

## Maintain adopter documentation

When a command, default, schema or result contract changes, update its help and
the [CLI reference](cli.md) together, including targets, side effects, errors,
exits, cancellation, framing and unknown/partial outcomes. Update affected
[adoption](adoption.md), [daily-use](operations.md#daily-loop) and runnable example
paths in the same change. Keep contributor build/test/release tooling in its own
guides; current adopter contracts must not depend on milestone history or Go types.

Keep each rule in one authoritative reference and link to it; preserve inbound
anchors when moving sections. Project examples must use declared private inputs
and portable tool names rather than secrets or machine-specific paths. Provider
settings belong in external machine configuration. Reuse runnable recipes rather
than adding parallel acceptance harnesses to tutorials.

Check help and shared types/schemas against the executable revision being audited,
check local links/anchors, and exercise affected success/failure paths with owned
fixtures and authenticated cleanup. Record revision, commands, results and unavailable
provider/platform evidence outside the committed tree. For docs-only changes,
run `make check-docs` and `git diff --check`; select existing example checks for the
changed guidance, without claiming skipped checks passed.
