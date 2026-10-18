---
title: 'Use Windows private IPC and filesystem identity'
date: '2026-10-18'
status: accepted
summary: 'Use local named pipes, explicit Windows ACLs and handle-derived filesystem identity to preserve the existing per-user daemon boundary on Windows.'
tags: [windows, ipc, privacy, identity, platforms]
supersedes: []
superseded_by: []
---

# Use Windows private IPC and filesystem identity

Use a Windows-only named-pipe transport and handle-based privacy/identity
adapter, preserving the shared typed HTTP/JSON API and macOS/Linux contracts.
Accepted on 2026-10-18 following maintainer review of the Windows design.
Implementation and runtime evidence remain required in #37; this decision does not
claim shipped support. The [Windows handoff](../windows-design.md) specifies the
implementation and evidence matrix. This Windows-only extension leaves the
accepted API and Linux decisions in force for macOS/Linux and does not supersede them.

## Context

The current client dials Unix sockets, localipc rejects Windows ownership and
Windows checkout identity returns unsupported. Unix mode bits, socket unlinking
and device/inode numbers cannot establish the intended Windows boundary.
The baseline is Windows 11 x64, PowerShell 7 and native Git/Go. Same-user hostile
code and administrator compromise are outside the existing trusted-user boundary.

## Decision

Use byte-mode local named pipes with explicit owner-SID DACLs, remote-client
rejection, first-instance protection, private generation metadata and a
nonce-bound authenticated handshake. Derive locators from user and state identity;
never treat a name as ownership. Preserve shared `/v1` framing/version checks.

Keep the current UserConfigDir default and explicit state-dir option. Require
local NTFS with private protected owner ACLs and reject unsafe existing ACLs,
reparse points or unsupported paths without repair. Use exclusive LockFileEx
startup-through-close locking and retain the file. Closed pipe handles require
no stale filesystem unlink; occupied or uncertain endpoints remain untouched.

Derive checkout/project/Git identity from open directory handles, volume identity
and file IDs, with final paths for move detection. Revalidate snapshot identity
before acceptance. Alias convergence must respect actual case sensitivity;
worktrees remain distinct and moves/replacements cannot adopt retained state.
Implementation must close substitution races at private-file and mount boundaries.

## Consequences

Native Windows can preserve per-user local access without a TCP admin listener
or elevated installer. ACL/handle correctness and cross-user denial require actual
Windows tests. Failures are conservative and may require operator diagnosis.
This documentation change adds no dependencies or runtime implementation.
Release/support claims remain pending #37/#38 and exact revision-bound evidence.

## Alternatives considered

AF_UNIX on Windows preserves socket syntax but does not remove the need to prove
Windows ACL, peer, locking and stale-endpoint behavior. Named pipes have explicit
local access/security controls and mature Go adapters to evaluate. Loopback TCP
adds bearer-secret exposure and a listening network endpoint unnecessarily.
User SID alone does not authenticate a server generation. Logon-SID-only access
would change per-user cross-session behavior. Path strings/lowercasing and chmod
cannot establish filesystem identity or privacy. A named mutex alone does not bind
startup ownership to the private state file and introduces namespace squatting.

## Revisit when

Native Windows experiments cannot satisfy the private ACL/handshake or file-ID
contract, roaming/network state becomes required, or cross-session/elevation
requirements change. Resolve product consequences before dependent implementation.

## References

- [Windows implementation and acceptance handoff](../windows-design.md)
- [Accepted typed local API](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Accepted Linux platform boundary](20261016T201500000Z-support-explicit-linux-managed-https-reachability.md)
- [Microsoft named-pipe rights and default ACL caveats](https://learn.microsoft.com/en-us/windows/win32/ipc/named-pipe-security-and-access-rights)
- [Microsoft CreateNamedPipe flags](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createnamedpipea)
- [Microsoft file ID and volume identity](https://learn.microsoft.com/en-us/windows/win32/api/winbase/ns-winbase-file_id_info)
- [Microsoft LockFileEx](https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-lockfileex)
- [Epic 34](https://github.com/octacian/backlot/issues/34), [native slice 37](https://github.com/octacian/backlot/issues/37)
