---
title: 'Register private project configuration'
date: '2026-10-21'
status: superseded
implementation: implemented
summary: 'Register project-only private inputs and tool overrides outside checkouts, reuse them across linked worktrees, and isolate CI setup without redirecting application configuration.'
tags: [configuration, security, identity, cli, persistence, ci, compatibility]
supersedes: [/docs/adr/20261020T001700474Z-recover-absent-native-sessions-without-cleanup-receipts.md]
superseded_by: [/docs/adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md]
related: []
tracking: ["https://github.com/octacian/backlot/issues/48"]
implemented_by: ["https://github.com/octacian/backlot/commit/1f54d4ca3e6c1208c3c771809b6f1064bf3dac32"]
---

# Register private project configuration

## Context and Problem Statement

Machine-wide input values collide across projects. Private worktree files require
copying and risk commits; per-command private-file flags make normal operation
fragile. Offline planning needs credentials without a daemon, and linked worktrees
should share setup without sharing runtime identities or redirecting application
configuration in CI.

## Considered Options

- An independent per-user registry with immutable private generations.
- Ignored checkout files or per-command flags: repeat selection/copying.
- Project-name/remote matching or automatic import: invent credential association.
- A mutable secret file or daemon-backed service: weakens coherent offline reads
  or adds transaction/service scope without a demonstrated need.
- HOME/XDG redirects in CI: also redirect unrelated tools and explicit app settings.

## Decision Outcome

Only explicit project setup enrolls private inputs and tool overrides. Associate
Git projects by common-directory filesystem identity and manifest-relative location;
linked worktrees share, independent clones/projects do not. Non-Git projects use
canonical directory identity. Moves/replacements require explicit remediation;
names/remotes never grant credential reuse. Read-only resolution never enrolls.

Keep the registry independent of daemon state and outside checkouts. Publish
immutable owner-private generations through one atomic registry replacement, with
revision/identity checks under a stable lock. Failed precommit edits preserve active
state; postcommit durability/response failure may be committed-but-uncertain.
Retain old generations for readers rather than adding background repair/pruning.

Private documents are the sole input-value source; machine configuration retains
providers, storage and HTTPS. Strictly validate each source independently. Tool
precedence is explicit, generation occurs only on setup request, and incomplete
updates cannot publish. Capture one generation for preparation/launch and preserve
recorded versus current provenance. Never rewrite application configuration or
invent historical values/digests from current files.

Reusable Actions invoke the existing setup CLI with bounded stdin values and a
job-private registry selector. Keep secrets out of argv, child payload variables,
public outputs and diagnostics; masks are a backstop. Join owned consumers before
private cleanup, leaving application HOME/XDG and unrelated tooling unchanged.

The original v6-to-v8 transition preserved retained state in one transaction and
changed only its compatibility fence. Later migration decisions replace that
transition policy, retaining configuration/privacy and native-recovery guarantees.

### Consequences

One explicit setup supports worktrees without a new secret service or background
owner. Operators must manage obsolete generations and rotation does not erase old
secrets from snapshots/backups. Local filesystem locking/atomic rename and a trusted
user are assumptions; unsupported/malformed state fails without automatic repair.

## More Information

[Setup and private configuration](/docs/manifest.md), [CI boundary](/docs/ci.md),
[registry publication](/internal/projectconfig/store.go).
