---
title: "Select bounded working directories for native commands"
date: 2026-11-03
status: accepted
implementation: in-progress
summary: "Select project-contained native command directories without changing other path anchors or retained lifecycle guarantees."
tags:
  - native
  - planning
  - compatibility
supersedes: []
superseded_by: []
related: [/docs/adr/20261023T211408955Z-explain-native-tool-selection-without-probing-runtimes.md, /docs/adr/20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md]
tracking: ["https://github.com/octacian/backlot/issues/110"]
implemented_by: ["https://github.com/octacian/backlot/pull/118"]
---

# Select bounded working directories for native commands

## Context and Problem Statement

Package-local native entry points need their package directory to interpret
relative arguments and discover application configuration. Today every native
command starts in the manifest directory, requiring application wrappers solely
to change directory. Changing all project-relative anchors would break existing
tools, declared outputs, evidence and configuration.

## Considered Options

- Add a bounded directory to the existing native command contract.
- Keep explicit shell or application wrappers: no Backlot change, but retains the
  adapter that this feature is intended to remove.
- Move the manifest into each package: works for independent projects, but splits
  a scene that coordinates packages and changes project identity and path anchors.
- Rebase every path or add a launcher framework: broader behavior and maintenance
  than selecting a process directory requires.

## Decision Outcome

Add optional `command.working_directory` for native jobs and services, relative
to the manifest directory. Omission preserves that directory. Command readiness
probes on native services inherit the service's selected directory; an explicit
override is independently manifest-relative, and `.` selects the manifest directory.
Use bounded relative path syntax consistent with project paths, additionally
allowing `.`; reject absolute paths, traversal and invalid path forms.

Offline planning resolves selected directories using filesystem metadata only,
requiring an existing directory whose canonical target remains inside the
canonical manifest directory. Retain the declaration and effective canonical
selection through the existing plan and private snapshot. Recheck containment,
existence and agreement with that selection before workload activation; a changed
target fails rather than silently redirecting a prepared command. Set each child
process directory through the native adapter, never process-global `chdir`.
This is cooperative execution, not filesystem containment against concurrent
host mutation or access performed by the application itself.

Keep relative executable selection, discovery, output, artifact and seed anchors
unchanged. Relative argv paths are interpreted by the invoked application from
its selected cwd; authors must adjust those arguments and declared evidence paths
when selecting a package directory. Environment inheritance is unchanged.

Missing cwd fields in older retained snapshots mean the historical manifest
directory, including probes. Treat equivalent default selections alike in retained
compatibility comparisons without rewriting historical snapshots or adding a
state migration. Preserve the existing snapshot acceptance transaction and
restart policy: ordinary command/probe cwd changes are compatible; changes to a
retained fresh-only initializer's effective cwd require reset/destroy. Healthy
work keeps its accepted selection and reports unapplied drift. Compare effective
cwd selections as well as manifest bytes so an in-project symlink retarget is
visible drift even when the manifest digest is unchanged; a compatible new
attempt may explicitly accept the new target through existing start/restart rules.

Container image `WORKDIR` remains unchanged. Container services already support
host-native command probes: preserve their manifest-directory default when
omitted and support the same bounded manifest-relative overrides. These commands
run on the host, independently of the container image directory.

### Consequences

Applications can invoke package-local commands directly while retained defaults
and other path contracts stay stable. The existing planner, daemon snapshot
writers, launch adapter and drift checks own the additional field and validation;
there is no new dependency, background work, endpoint or automatic retry.
New fields remain optional when unused. Maintainers must preserve default
normalization and distinguish symlink target drift from manifest-byte changes.
Directory replacement at the same canonical path and hostile concurrent filesystem
mutation do not gain new inode-pinning or sandbox guarantees.
