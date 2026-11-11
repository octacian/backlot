# Architecture decision records

ADRs preserve consequential choices among real alternatives: boundaries, contracts,
dependencies, security and durable conventions. Routine details already governed by
an accepted decision or practice guide need no new record.

## Keep decisions short

Record one decision, its context, credible options, reason and consequences.
Keep only implementation detail needed to understand an architectural commitment
or tradeoff. Put API field inventories and current behavior in reference guides,
algorithms and invariants beside code, scope and steps in issues/epics, and test
commands, rollout checklists and execution evidence in PRs. Link rather than copy.
Do not require a separate overview, revisit section or references section when
that would repeat information. Optional sections must earn their space. Review
for repeated explanations and delivery details before adding more prose.

This follows the concise, single-decision approach in
[Nygard's ADR guidance](https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions)
and [MADR](https://adr.github.io/madr/). Brevity is a review responsibility, not a
word-count gate; retain material rationale and security/ownership constraints.

## Consult decisions

Search filenames and frontmatter before architectural or cross-cutting work:

```sh
rg -n '^(title|summary|tags|status|implementation):' docs/adr
```

Apply relevant accepted decisions after checking their claims against current code.
Proposals and rejected records are context, not authority. Deprecated records no
longer apply; follow supersession links to the current decision. Acceptance and
implementation are independent; metadata alone is not proof of shipped behavior.

## Create and check

The [Corestack docs-format specification](https://gitea.auvem.com/corestack/go/src/tag/v0.2.0/docs/docs-format.md)
owns record format, templates and validation. Generate records with
`go run go.auvem.com/corestack/cmd/corestack-docs@v0.2.0 new adr "Decision title"`
from the repository root. Replace all generated prompts; maintain no local template.
Run the pinned documentation command in [AGENTS.md](/AGENTS.md) or `make check-docs`.
The repository owns its version pin; upgrades must update the documented commands,
Makefile and both forge workflows together and verify existing records.

The documentation tier is ADRs only: issues/epics are sufficient delivery context.
Use a separate plan only when cross-area or multi-session work needs durable
execution context, enabling that tier deliberately rather than adding empty files.

## Lifecycle and reconciliation

Create proposals for independent review and owner acceptance; an agent or a clean
check cannot accept a decision. Revise records introduced in the same unmerged PR
in place. For material changes to merged accepted choices, create a replacement;
while proposed, link the old record through `related`. On owner acceptance, update
reciprocal supersession links together. Preserve filenames and historical rationale.

Before final review and merge, compare the latest target branch plus the change
with affected decisions. Update implementation state and `implemented_by` only
with real delivery evidence, using the PR URL when available. Do not invent
tracking, acceptance or completion; leave unfinished scope visible in issues/epics.
Run the pinned checker after reconciliation. It checks records and local file
links, not decision quality, link fragments, owner approval or runtime correctness.

The Docs workflow runs on every PR and push to main, including code-only changes
that can break record links. Validation requires no credentials for the public
BSD-licensed checker; network/module-cache access is needed on a cold installation.
