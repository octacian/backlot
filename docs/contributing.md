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
