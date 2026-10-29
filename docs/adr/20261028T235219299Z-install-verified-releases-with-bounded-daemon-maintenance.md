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

Use a stock-shell downloader and a temporary foreground helper in the verified
target executable. The shell verifies and stages the release; the helper obtains
consent, uses the compatible old CLI for daemon cleanup, and holds selected-state
ownership through atomic replacement. Preserve retained state and leave the daemon
stopped for explicit startup with the new binary.

## Context

[Issue #101](https://github.com/octacian/backlot/issues/101) needs installation and
updates without Go or a package manager, including an instance count, listing and
consent before stopping a running daemon. Broader infrastructure controls remain
in [#97](https://github.com/octacian/backlot/issues/97).

Existing cleanup joins resources before acknowledging `stopping`, but final
socket/database/lock teardown follows. Failed status cannot distinguish absence
from unsafe access, and stock shell tools cannot portably hold the existing lock
on both supported platforms. The verified target supplies typed inspection and
locking without another cleanup engine or an installed JSON parser.

## Decision

- Reuse published macOS ARM64/Linux amd64 archives and checksums. Fresh installs
  accept latest stable or an exact canonical helper-bearing tag; existing installs
  update only to latest stable. Refuse explicit-version updates, downgrades,
  unsafe destinations and unrecognized executables. Use the target itself as the
  helper; older helperless releases require manual installation, never a second
  helper download or unpublished/source fallback.
- The shell owns private download verification and destination-local staging.
  Validate archive contents and executable identity before effects; replacement
  is one atomic rename after revalidation. Apply the inherited release trust
  policy: checksums detect corruption, not publisher authentication. Installation
  options and procedures belong in the [installation guide](../operations.md#release-installer-and-updates).
- The helper performs bounded, validated inspection and cleanup through the
  retained old executable's compatible CLI, not the new client's state-specific
  wire contract. Compatibility does not establish the daemon's exact release.
  Keep the old executable at its original path until handoff completes; decoded
  observations are informational, never cleanup authority.
- Operate on one default/explicit state root. A verified absent daemon needs no
  consent; a running daemon requires terminal consent with observed running/kept
  and separate unresolved-cleanup counts. `y` attempts shutdown, `n` cancels, and
  `l` lists affected instances before asking `y`/`n` again. Read the controlling
  terminal for piped installs too; refuse required consent without one.
- Require successful old-CLI cleanup for a running daemon, then acquire its existing
  state lock under a finite budget. Hold that lock through file/ownership checks
  and rename on both running and absent paths; preserve its inode. Creating the
  standard missing private root/lock is allowed to exclude concurrent startup.
  Do not open or migrate the database.
- Failed or uncertain cleanup, lost replies, timeout or pre-rename cancellation
  preserves the old executable and diagnostic state. Forward cancellation and
  join owned children before temporary cleanup; already accepted shutdown may
  finish. Preserve data, inputs, CA identity, evidence and project pins. Leave the
  daemon stopped without force or automatic recovery/restart.

Successful cleanup plus held ownership is the handoff guarantee. Neither a stop
acknowledgment nor lock release alone proves it; it does not establish a historical
daemon's final exit code or universal descendant absence. Existing native cleanup
limits remain unchanged. The [CLI reference](../cli.md) owns exact helper arguments,
validation, deadlines and framing.

## Consequences

The helper adds a local CLI compatibility obligation and temporary concurrent-start
exclusion, with no new wire API, persisted format, background service or resource
manager. Writes are limited to the executable, owned temporary files and any missing
standard private state root/lock; consented cleanup retains existing daemon writers.
Keep that lock after installation. Dependencies and trust remain operator setup.

A verified same-version rerun may no-op without daemon maintenance.

## Alternatives considered

- **Go installation/manual archives:** available, but do not provide the requested
  download-and-update interaction with a running daemon.
- **Shell plus existing commands alone:** lacks portable held-lock replacement and
  reliable typed inspection; parsing human output creates another compatibility
  dependency.
- **Separate latest helper for historical targets:** adds an artifact and compatibility
  obligation that fresh-only exact installs avoid.
- **Wait for or implement #97:** postpones the requested interaction or expands into
  infrastructure orchestration; existing cleanup suffices without new receipts
  or a second shutdown engine.

## Revisit when

#97 supplies a reusable completion contract, observed failures require additional
recovery, or distribution/signing requirements change.

## References

- [Release policy](20261026T020229927Z-reuse-exact-main-ci-and-prepare-verified-release-drafts.md) and [inherited archive contract](20261023T212448687Z-publish-immutable-versioned-releases-with-goreleaser.md)
- [Retained migrations](20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md)
- [Helper](../../internal/daemon/install.go), [daemon cleanup](../../internal/daemon/service.go), [daemon ownership](../../internal/daemon/server.go)
- [Installation](../operations.md), [CLI](../cli.md), [daemon](../daemon.md)
