---
title: 'Install verified releases with bounded daemon maintenance'
date: '2026-10-28'
status: accepted
summary: 'Use verified release downloads and a bounded foreground helper to retain selected-state ownership through atomic executable replacement.'
tags: [installer, release, daemon, compatibility, security]
supersedes: []
superseded_by: []
---

# Install verified releases with bounded daemon maintenance

Provide one downloadable shell installer/updater for published macOS ARM64 and
Linux amd64 releases. Verify the selected archive and executable before replacing
one explicit destination. A temporary foreground helper owns typed old-CLI
inspection, terminal consent, bounded selected-state locking and the fixed final
rename. Preserve retained state and leave a consented running daemon stopped.

## Context

[Issue #101](https://github.com/octacian/backlot/issues/101) requests a release-based
installation path without Go or a package manager. The approved delivery requires
interactive shutdown, including instance count and listing; the broader coordinated
infrastructure interface in [#97](https://github.com/octacian/backlot/issues/97)
remains separate. Existing release archives/checksums and lifecycle implementations
are reusable; the installer must not implement a second resource-cleanup engine.

The stop endpoint joins resource cleanup before replying, but returns `stopping`
before socket/database/lock teardown. Failed status cannot safely mean absence:
missing sockets and unsafe transport can share a permissions error. Shell tools
on both supported targets cannot portably acquire the daemon's existing lock.
Successful compatible old-CLI cleanup followed by exclusive selected-state
ownership is the installation safety boundary; the installer does not need a
new provider cleanup engine or every historical daemon's final exit result.

## Decision

- Default to GitHub's latest published stable release; accept an exact canonical
  stable version for fresh installations only and destination (default
  `~/.local/bin/backlot`). Existing installations update only to latest stable;
  an explicit version is rejected before lifecycle effects. Reuse existing
  versioned archive/checksum names and supported targets. Refuse downgrades,
  unrecognized existing executables, unsafe destinations and uncertain selection.
- Use private owned download/staging directories, validate archive membership and
  entry types before extraction, require exactly the selected checksum entry,
  and verify executable release/API identity. Stage on the destination filesystem
  and rename atomically after all checks. Preserve the prior executable on every
  pre-replacement failure; clean only installer-owned temporary files. Report
  which executable PATH selects, including shadowing, without editing profiles.
- Use one foreground helper from a verified helper-bearing release executable.
  Shell owns release selection, download verification and destination-local
  staging; the helper owns inspection, consent and the fixed atomic rename.
  Capability-check the helper before lifecycle effects. Keep a recognized existing
  executable available at its original path through successful shutdown and
  replacement. Execute it directly with bounded subprocess calls; use its existing
  compatible `daemon status`, `list`, `inspect` and `daemon stop --json` contracts.
  Compatibility is not proof of the daemon's exact release string. Do not send
  the new binary's state-version-specific client directly to an older daemon.
  Bound inspection and subprocess operations; cancellation must join owned children.
- Decode old-CLI results using named shared Go types and validate API, result
  identities, statuses and continuation progress. Paginate every list read and
  inspect records as necessary to include kept failures. The warning count covers
  observed executing, starting, stopping and kept workloads, excluding metadata-only
  prepared/history records; show unresolved cleanup separately. Display bounded,
  terminal-escaped instance details. Malformed, partial, incompatible or unsuccessful
  results cancel installation. These are live observations, not an atomic snapshot
  or teardown authority.
- Select one explicit/default state root without scanning unrelated roots. A
  verified absent daemon permits immediate upgrade. A running daemon requires the
  observed count and a `y`/`n`/`l` warning prompt; `n` cancels, `y` attempts shutdown,
  and `l` lists observed affected instances then asks `y`/`n` again. Read from the
  controlling terminal, including when the shell installer is piped; cancel when
  consent is required without a terminal. Consent authorizes only this selected
  daemon's existing stop operation.
- For a running daemon, require a successful compatible old-CLI stop response,
  then acquire the selected root's existing `daemon.lock` under a finite deadline.
  Retain that lock in the same foreground process through destination/staged-file
  revalidation and the final atomic rename; release it afterward. Take the same
  critical section on the absent-daemon path. Reuse private-directory/file validation
  and the existing lock implementation; never unlink or replace its inode. Creating
  the standard private state directory/lock when missing is allowed to exclude
  concurrent startup. Do not open or migrate the database. Unsafe or ambiguous
  ownership/transport, failed cleanup, lost stop response, timeout or cancellation
  before rename preserves the old executable and diagnostic state.
- Describe completion as successful cleanup under the existing daemon contract
  plus acquired/held state ownership. Neither acknowledgment alone nor lock release
  alone establishes both. Lock release cannot recover the historical daemon's final
  exit code or prove cleanup after a crash; preserve existing native `observed_empty`
  limitations. Do not claim universal process absence or exclusion of later explicit
  starts. Leave the daemon stopped; no force, automatic restart, generic command
  runner, lock-handoff protocol, new daemon API or persistent receipt is introduced.
- Preserve state, inputs, CA identity, data, evidence and consuming-project pins.
  No state migration or new durable receipt belongs to installation. Checksums
  detect corruption, not publisher authentication; unsigned/unnotarized binaries
  retain explicit platform security-policy decisions. Add no sudo, dependencies,
  trust changes, package-manager integration, background updater or publication.

## Consequences

The installer has no idle/background behavior or new dependency. Its writes are
the selected executable, owned temporary files and, if missing, the standard
private selected-state directory/lock. Preserve that lock rather than removing it
as temporary state. Consented lifecycle calls retain existing daemon writers.
No new persisted format, registry, migration, timer, cleanup authority or provider
adapter is added. The local helper adds CLI compatibility and brief concurrent-start
exclusion obligations, with bounded cancellation and subprocess ownership.

Existing published releases lack this helper. Source implementation must first
be released through the ordinary release process before a public curl path can
work; this change does not authorize publication. Fresh exact targets must contain
the helper; otherwise refuse with manual archive installation guidance before
lifecycle effects. Always use the verified new target itself as the helper, with
no second binary/download or source/unpublished fallback. Latest reruns may no-op
after identity verification when the installed version already matches.

Ship focused corruption/replacement, typed historical-output, consent, pagination,
failure and concurrency coverage; update CLI help and canonical installation/agent
guidance, applicable CI filters and generated skill references together. Runtime
evidence and measured scope belong in the delivery report, not this record.

## Alternatives considered

- **Go installation or manual verified archives:** already available and smallest
  operationally, but do not provide the requested download/update procedure.
- **Shell installer plus existing commands alone:** suitable for download and
  replacement; lacks portable lock proof and robust kept-instance inspection.
  Human row parsing is cheap but would create an undocumented output dependency.
- **One downloaded-binary foreground helper:** chosen because typed old-CLI reads
  and lock-held rename reuse existing Go contracts without jq/Python, shell JSON
  parsing or a separate lock-holder protocol. Helperless historical tags require
  manual archive installation; separately downloading a latest helper adds an
  unnecessary artifact and compatibility obligation.
- **Wait for #97:** avoids a temporary lifecycle interface but defers the approved
  running-daemon UX. Fail closed with matching-binary recovery guidance meanwhile.
- **Implement unified infrastructure or persistent shutdown receipts now:** expands
  #97, introduces more shared behavior/writers and exceeds this installer's scope.

## Revisit when

#97 supplies a compatible completion contract, real fixtures require restart-based
recovery, instance inspection needs another API, measured growth exceeds the
agreed boundary, or signing/distribution requirements change.

## References

- [Release contract](20261026T020229927Z-reuse-exact-main-ci-and-prepare-verified-release-drafts.md) and [inherited archive contract](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md)
- [Retained migrations](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Daemon shutdown/lock](../../internal/daemon/server.go), [cleanup](../../internal/daemon/service.go), [IPC validation](../../internal/localipc/permissions.go), [list pagination](../../internal/daemon/list.go)
- [Installed release fixture](../../internal/daemon/installed_integration_test.go) and [managed runtime fixture](../../internal/daemon/managed_gateway_integration_test.go)
- [Installation](../operations.md), [daemon](../daemon.md), [HTTPS](../https.md), [platforms](../platforms.md), [agent guidance](../agent-skills.md)
