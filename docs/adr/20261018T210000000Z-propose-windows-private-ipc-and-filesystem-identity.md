---
title: 'Use Windows private IPC and filesystem identity'
date: '2026-10-18'
status: deprecated
implementation: not-applicable
summary: 'Deprecate the unimplemented native Windows design because demand does not justify its platform maintenance and verification cost.'
tags: [windows, ipc, privacy, identity, platforms]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/34"]
implemented_by: []
---

# Use Windows private IPC and filesystem identity

## Context and Problem Statement

Unix sockets, mode bits and inode identity cannot establish Windows private IPC or
filesystem ownership. Native Windows would require separate adapters and sustained
platform verification, not merely cross-compilation.

## Considered Options

- Private local named pipes and handle-based filesystem identity.
- Windows AF_UNIX: retains the ACL/peer/locking problems.
- Loopback TCP: adds a listening administrative endpoint and credential exposure.
- Path strings, chmod or names alone: cannot authenticate ownership or replacement.

## Decision Outcome

Historically, choose per-user local named pipes with protected owner ACLs,
remote-client rejection and generation-bound authentication. Require private local
NTFS state and exclusive retained file locking. Derive project/checkout identity
from open handles, volume/file IDs and canonical paths; reject unsafe ACLs,
reparse points, moves and substitutions rather than repairing or adopting them.
Preserve the shared typed API and existing macOS/Linux contracts.

This decision was accepted in October 2026 but never implemented. It was deprecated
on 2026-10-28: maintainer-assessed demand does not justify native Windows runtime,
provider and desktop maintenance. No native Windows support shipped or remains
promised. Reconsider only with demonstrated demand and ongoing maintainer capacity.

### Consequences

The design could preserve per-user local administration without TCP or elevation,
but its ACL, peer and replacement guarantees require actual Windows evidence.
Deprecation removes that delivery obligation; the historical design is context,
not current implementation guidance.

## More Information

[Current platform policy](/docs/platforms.md),
[historical design](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/windows-design.md).
