---
title: 'Publish daemon-owned portable run reports'
date: '2026-10-18'
status: proposed
summary: 'Export a fenced settled attempt through the typed daemon API into an audited no-replacement directory with an independently versioned report.'
tags: [evidence, api, security]
supersedes: []
superseded_by: []
---

# Publish daemon-owned portable run reports

The daemon owns portable evidence export through the existing same-user local
API. Publish one settled execution attempt as a directory containing an intentional
`backlot.export/v1` JSON report, a script-free HTML entry page and copied evidence.
Clients and Actions consume this public operation rather than daemon storage.

## Context

[Issue 41](https://github.com/octacian/backlot/issues/41) requires useful reports
after private runtime removal and relocation. Existing Actions duplicate private
record selection, copying and audit; active producers and concurrent pruning can
otherwise produce misleading evidence. Report presentation must not execute
untrusted summary text or serialize private lifecycle capabilities.

## Decision

Fence instance mutations and evidence pruning while selecting and copying the
latest settled attempt. Wait is explicit and observes without run cancellation;
kept services are producers until stopped. Strict export rejects known incomplete
evidence. Explicit partial mode records omissions and never relaxes safety.

Use a new caller-owned destination outside private daemon state, sibling private
staging, and platform atomic no-replacement rename. Existing destinations are
always rejected. Root filesystem operations anchor copy/destination access.
Audit generic filesystem, key and ZIP exclusions before publication; application
format assertions remain in the existing application tests, without a product
parser framework. Preserve application report bytes and assets as labelled content.

Version the portable report separately from API/state. Inventory only copied
relative paths and hashes, bounded provenance, timestamps and original outcomes.
Do not serialize plans, machine paths/configuration, secrets, fixtures or private
records. Missing historical facts remain unknown. A publication failure may
return a settled original result alongside its typed error; it never claims a
published destination. No historical compatibility/migration layer is introduced.

## Consequences

One operation works for local users, agents and CI. Caller storage determines
retention after publication. Export holds lifecycle/pruning fences during copying;
large evidence can delay those operations. The HTML summary requires no runtime
or scripts, while application reports retain their own viewer requirements and
application secret discipline remains necessary.

## Alternatives considered

Action-side export keeps private storage coupling and leaves local users without
a supported workflow. Linking evidence fails relocation and pruning. Replacing
an existing directory risks data loss; check-then-rename alone cannot enforce
collision behavior under races. A framework report parser/UI adds an unrelated
product boundary. Archives are unnecessary for the minimum portable directory.

## Revisit when

Revisit snapshot cost if measured evidence volume makes copying under fences
unacceptable, or when a new supported platform needs an equivalent safe atomic
publication primitive. Binary releases and action version tags remain issue 25.

## References

- [Shared API architecture](../architecture.md#shared-api)
- [Portable export contract](../evidence.md#portable-per-instance-export)
- [Typed Go API decision](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
