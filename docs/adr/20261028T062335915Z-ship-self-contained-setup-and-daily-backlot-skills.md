---
title: 'Ship self-contained setup and daily Backlot skills'
date: '2026-10-28'
status: accepted
implementation: implemented
summary: 'Distribute two procedural skills with generated local references and explicit executable compatibility checks so adopter agents can use Backlot without its source checkout.'
tags: [skills, documentation, packaging, compatibility, agents]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/59"]
implemented_by: ["https://github.com/octacian/backlot/commit/6efda8051396286a95903dd2189a4ce20073e1de"]
---

# Ship self-contained setup and daily Backlot skills

## Context and Problem Statement

Adopters need setup and daily-use procedures without a Backlot checkout, manual
copy drift or requiring both skills. Remote repository links alone cannot supply
essential offline guidance.

## Considered Options

- Independent procedural skills with generated local references.
- Hand-copied manuals: drift from maintained contracts.
- Sibling links or every guide in each bundle: break independent installs or add
  unused context.
- Exact build allowlists: exclude usable development builds and need continual updates.

## Decision Outcome

Ship separate setup and daily-operation entry points. Deterministically generate
ordinary local files from canonical guides, schemas and examples; preserve offline
procedures and local links, and reject drift in CI. Keep entry points short and
load references by need.

Distribute through Git sources and the optional installer, supporting selective
project/global installation and immutable refs. Users choose installation/refresh;
no automatic updater or runtime distribution service is added.

Check executable identity, required read capabilities and targeted help rather
than trusting version labels alone. Development builds remain usable with those
checks and user-managed refreshes; capability/help checks cannot prove semantics
or provider readiness. Skills inherit ownership, disclosure and result guarantees
and never expand command authority.

### Consequences

Generated duplication permits independent installs from one maintained source.
Maintainers own selection, generation, drift checks and evaluation; users own
runtime/skill pairing. There is no new background work. Revisit when evaluation
finds missing guidance, overlapping triggers or incompatible installer behavior.

## More Information

[Compatibility/installation](/docs/agent-skills.md),
[generation/evaluation](/docs/testing.md#agent-skills).
