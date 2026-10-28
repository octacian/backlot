---
title: 'Ship self-contained setup and daily Backlot skills'
date: '2026-10-28'
status: accepted
summary: 'Distribute two procedural skills with generated local references and explicit executable compatibility checks so adopter agents can use Backlot without its source checkout.'
tags: [skills, documentation, packaging, compatibility, agents]
supersedes: []
superseded_by: []
---

# Ship self-contained setup and daily Backlot skills

Ship independent `backlot-setup` and `backlot` skills with local references
for using the existing CLI without a source checkout.
Allow development builds after capability/help checks, with user-managed refreshes.

## Context

[Issue 59](https://github.com/octacian/backlot/issues/59) needs adoption and daily-use
procedures without network access, manual drift or requiring both skills.

## Decision

- Setup adapts application commands/declarations; daily use operates configured
  scenes; keep entry points short and load references by need.
- Deterministically generate ordinary files from canonical guides, schemas and
  examples inside each bundle. Preserve local links/anchors and offline essential
  procedures; CI rejects drift. Update sources and regenerate in the same PR.
- Distribute through repository Git sources and the optional skills installer,
  supporting selective project/global installs and immutable refs, without new
  runtime/API/distribution machinery or automatic installation/update behavior.
- Check executable identity, required local/daemon read capabilities and targeted
  command help as specified in [compatibility guidance](../agent-skills.md#compatibility).
  Allow `dev`/`dev-dirty`; these checks cannot prove semantics or provider readiness.
  Users of global development builds must refresh skills with runtime changes.
- Inherit [ownership/data](../daemon.md#identity-and-lifecycle),
  [disclosure](../daemon.md#declared-startup-details) and
  [result](../evidence.md) guarantees; skills do not expand command authority.

## Consequences

Generated duplication permits independent installs from one maintained source.
Maintainers own selection, regeneration, checks and evaluation; users own pairing.
Existing CLI commands retain runtime effects; skills add no background activity.

## Alternatives considered

- Repository links alone lack offline use; hand-copied manuals drift; sibling
  symlinks fail independent installation. Bundling every guide adds unused context.
- API/state versions or `dev` alone miss capabilities. Exact identity allowlists
  exclude usable development builds and impose ongoing identity updates.

## Revisit when

Evaluations expose missing references, overlapping triggers or incompatible
workflows; installer copy behavior changes; or a consumer cannot use a shell.

## References

[Installation/compatibility](../agent-skills.md), [generation/evaluation](../testing.md#agent-skills).
