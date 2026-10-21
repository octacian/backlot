---
title: 'Select a private project registry without redirecting application configuration'
date: '2026-10-21'
status: accepted
summary: 'Select the project registry directly for Backlot children so CI isolation preserves application HOME, XDG and explicit environment pass-through semantics.'
tags: [configuration, security, ci, compatibility]
supersedes: [20261021T035107160Z-use-project-only-input-values-and-private-ci-project-setup.md]
superseded_by: []
---

# Select a private project registry without redirecting application configuration

Use optional `BACKLOT_PROJECT_CONFIG_DIR` to select the direct private project
registry directory, leaving `HOME` and `XDG_CONFIG_HOME` unchanged. Actions select
an authenticated job-owned directory for every Backlot child and check runtime
support before configuration resolution. The default remains
`os.UserConfigDir()/backlot` when the selector is absent.

This correction replaces the [project-only input decision](20261021T035107160Z-use-project-only-input-values-and-private-ci-project-setup.md)
only in its registry selection mechanism and runtime support check. Retain its
sole-source input contract, bounded action transport and file-publication boundary,
state8 fence, registered-configuration lifecycle, application content responsibility
and all unaffected native recovery guarantees. The supersession chain preserves the predecessor's accepted rationale.

## Context

A child-scoped `XDG_CONFIG_HOME` selects the private registry on Ubuntu, but the
daemon also resolves explicitly selected application environment pass-through.
A valid manifest selecting `XDG_CONFIG_HOME` would then receive the action's
internal directory instead of the application's original value. The launch baseline
normally excludes XDG, which does not protect that explicit case. Rejecting such a
manifest would introduce a restriction contrary to the approved requirement to
preserve application environment semantics. The existing HOME/XDG selection also
serves machine configuration and unrelated tools, beyond the project registry.

The coordinator authorized a direct registry selector after independent AR02
reassessment. No new project input layer, registry format, service, migration,
background worker, CLI flag or general environment sanitization policy is needed.

## Decision

When `BACKLOT_PROJECT_CONFIG_DIR` is present, require a nonempty absolute path to
the direct registry directory containing `projects.json` and `projects/`. An
explicit empty, relative, invalid or unsafe selection fails without default
fallback. When absent, retain the existing `os.UserConfigDir()/backlot` default.
Reuse one selection helper for storage and error/provenance paths so errors never
attribute an explicit selection to the default registry. Preserve external-to-checkout
checks, existing ancestor resolution, final-component symlink rejection,
ownership/mode/regular-file validation and atomic publication/removal behavior.
An absent selected root remains a read-only absent registry; explicit setup alone
creates managed storage with the existing private modes and commit protocol.
Validate an existing selected root even if it has no registry file. Neither root
selection nor failure may read, write or enroll in the default registry as fallback.

The selector affects only private project registry/generation selection. Machine
configuration still uses its existing explicit path or original user-config
default. Do not redirect HOME/XDG, Git/Go/Node/browser/Caddy/SDK configuration or
workload configuration. Valid manifest-selected HOME/XDG pass-through retains its
original application value. Scope this selector to verified-owner Backlot child
environments, including setup/inspection, daemon/direct run, diagnostics/export
and cleanup/post paths; leave unrelated tool environments unchanged. The runtime
launch baseline does not include the selector. Preserve explicit application
pass-through rules rather than adding a general forbidden-variable policy. Raw
structured input payload environment variables remain excluded from all children.

Before any Backlot configuration resolution, setup checks the installed runtime's
existing `version --json` result, including installation-only mode. Add the shared
`state_version` integer metadata to `VersionResponse` and populate it from
`StateVersion` in CLI version reporting; no new endpoint or capability subsystem.
Require state version 8 and the expected API version before project setup or daemon
start. A historical pin without this metadata fails with sanitized compatibility
guidance before it can consult the operator registry. Source SHA verification and
existing machine/daemon compatibility checks remain independent checks.

## Consequences

The selector is a small public configuration contract with validation, selection,
provenance, documentation and regression obligations. The action can reuse the
existing synchronous registry under its owned external root while preserving
ordinary application environment behavior. No storage layout/version or persistence
lifecycle changes follow; cleanup still stops consumers before deleting the owned
root and preserves uncertainty. Private files remain excluded from publication,
while existing public redaction and application evidence responsibility remain.

Tests must cover absent/default and explicit selection, explicit empty/relative/
unsafe paths without fallback, selected-root diagnostic provenance, two-project
isolation and unchanged default files, HOME/XDG explicit pass-through, unrelated
tool environments, every Backlot child including post cleanup, and older runtime
rejection even without requested project setup. Earlier XDG-based native evidence
remains evidence of its tested input/runtime flow, not proof of this corrected
action selection mechanism. Implementation and independent review remain required.

## Alternatives considered

Child-scoped XDG requires fewer public names but redirects explicitly selected
application XDG values. Rejecting that pass-through breaks valid manifest behavior.
Separating daemon registry selection from its launch environment internally would
require retaining/restoring environment state across adapters and resolution; a
direct registry selector expresses the actual boundary with fewer moving parts.
A new CLI flag would need propagation through all adapters without improving this
job-scoped consumer. Probing old runtimes through setup or registry access risks
touching default storage before compatibility is known; version metadata checks
support without configuration resolution.

## Revisit when

A future state version changes this contract, or a supported runtime cannot select
private project storage independently of application configuration. Reassess the
selector explicitly rather than adding fallback or silent pin compatibility.

## References

- [Project-only inputs and private CI setup](20261021T035107160Z-use-project-only-input-values-and-private-ci-project-setup.md)
- [Project registry selection and provenance](../../internal/projectconfig/store.go)
- [Application environment resolution](../../internal/plan/environment.go)
- [Version contract](../../api/v1/version.go)
- [CLI version reporting](../../internal/cli/command.go)
- [Action process environments and cleanup](../../actions/setup/runtime.mjs)
- [Action setup](../../actions/setup/setup.mjs)
