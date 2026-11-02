---
title: 'Use Windows job-object supervision and managed HTTPS'
date: '2026-10-18'
status: deprecated
implementation: not-applicable
summary: 'Deprecate the unimplemented native Windows design because demand does not justify its platform maintenance and verification cost.'
tags: [windows, lifecycle, ownership, docker, https, trust]
supersedes: []
superseded_by: []
related: []
tracking: ["https://github.com/octacian/backlot/issues/34"]
implemented_by: []
---

# Use Windows job-object supervision and managed HTTPS

## Context and Problem Statement

Windows supervision cannot safely reuse Unix PID/group authority. A supported
native workflow would also need Windows console, Docker Desktop, Caddy and browser
trust evidence; cross-builds and WSL cannot establish those guarantees.

## Considered Options

- A retained guardian/job authority with creation-time process association.
- Create suspended then assign: guardian loss can strand an unassigned child;
  suspension is not termination authority.
- Taskkill/PID trees or named-job reopen: lose retained authority and race reuse.
- A privileged service: adds installation and authority beyond the chosen boundary.

## Decision Outcome

Historically, choose an outside guardian retaining a private non-breakaway,
kill-on-last-close job. Associate the root at creation time, verify through retained
handles before resume, and bound root/job/collector/guardian joins. Never treat a
failed create, closed port or guardian exit as absence proof. Unknown authority
retains quarantine and cleanup failure; recovery never signals observed PIDs or
reopens authority by name.

Preserve original exits independently of cleanup and collection. Keep private
authenticated control, explicit argument/shell handling and platform identity
checks. Use explicitly configured local Docker pipes and stock native Caddy,
with scoped routes, private loopback administration and explicit public-root trust.
No automatic widening, trust injection or TLS bypass is permitted.

Creation-time crash behavior, console/nested-job compatibility and actual Desktop
reachability remained unproven. This accepted design never shipped and was
deprecated on 2026-10-28 because demand does not justify ongoing native Windows
implementation and verification. It creates no current support or delivery promise.

### Consequences

Retained job handles could bound cooperative descendants without a hostile-workload
sandbox, but require substantial Windows-specific failure and desktop evidence.
Reopening the design needs demonstrated demand and maintainer capacity, followed
by renewed review of unresolved launch-authority and topology assumptions.

## More Information

[Current platform policy](/docs/platforms.md),
[historical design and acceptance scope](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/windows-design.md).
