---
title: 'Bind attached persistent execution to one daemon-owned attempt'
date: '2026-10-22'
status: superseded
summary: 'Keep optional attached persistent clients alive with an ephemeral daemon-owned attempt lease and existing retained log delivery.'
tags: [lifecycle, ownership, api, cli, logs]
supersedes: []
superseded_by: [20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md]
---

# Bind attached persistent execution to one daemon-owned attempt

`scene start --attached NAME` and `run --attached NAME` start a persistent scene,
show aggregate logs and remain until the owned attempt ends. The daemon grants
exclusive authority for that attempt; client cancellation or lease expiry stops
runtime and removes routes while retaining data and paired credentials. Ordinary
persistent starts and disposable runs keep their existing behavior.

Extend the existing execution owner, expiry coordination and retained-log API.
Keep attachment authority in daemon memory: daemon replacement already interrupts
runtime through recorded recovery, so attachment needs no durable registration,
state migration, replay service or automatic reattachment. This proposal extends
the lifetime and scene-targeting decisions without replacing their other rules.

Accepted after independent architecture review and coordinator approval on
2026-10-22; implementation and runtime verification remain separate obligations.

The [persistent-start decision](20261028T234801291Z-ensure-persistent-start-accepts-one-eligible-current-attempt.md) partially supersedes this record's ordinary-start eligibility and snapshot-selection rules; unaffected rules
and existing successors remain in force.

## Context

[Issue 50](https://github.com/octacian/backlot/issues/50) needs an editor play task
that receives scene output for its duration and stops its runtime on SIGINT or
SIGTERM. Signal handling alone cannot clean up after a killed client. A scene
name or persistent instance ID alone cannot authorize a delayed cancellation:
explicit restart can replace the execution under that same ID.

The daemon already serializes mutations, records execution attempts, supervises
service liveness, joins cancellation, removes owned routes and preserves persistent
resources. Disposable clients already have a finite lease; its duration defaults
to 30s and drives a shared expiry ticker and independently joined cleanup workers.
However, that stored lease is disposable-only and existing cancellation is by ID.
Reusing those APIs unchanged would either fail or risk stopping a replacement.
Logs already contain instance/component/attempt identity and support bounded pages.

The owner selected refusal of existing runtime, attempt/generation fencing,
existing lease timing and cleanup, sampled lifecycle output and existing aggregate
log semantics. Continuous readiness checks, stronger native absence guarantees,
automatic restart, reconnect and durable event history are outside this decision.

## Decision

### Acceptance and authority

Both attached entry points use the existing run coordinator and persistent-scene
identity. Reject disposable lifetime and incompatible disposable-only options or
terminal arguments before preparation/allocation. Reject attached restart/reset;
those remain independent operator commands. Default starts retain their current
join/readiness-return behavior. An ordinary start joining an attached attempt
does not acquire or extend its authority.

Under the existing instance mutation lock, grant attachment only when accepting a
new eligible execution, never by adopting a starting, ready, stopping or otherwise
independently owned runtime. Existing failure/restart and drift fences still
apply. Concurrent attached starts have one winner; a rejected request does not
cancel or alter it. A prepared or cleanly stopped persistent record can supply
the retained instance for a newly accepted attempt under existing start rules.

Bind a fresh private random capability and expiry to the in-memory execution
owner, instance ID, exact attempt and current daemon generation. Return authority
only to the accepting request, separately from ordinary snapshots and log output.
Never expose the capability in human/NDJSON output, inspection or diagnostics.
Acceptance installs the lease before launching runtime; loss of the response
therefore still leads to expiry cleanup. Do not retry acceptance or recover a
capability by inspecting state. No transaction spans provider work.

Add shared typed start opt-in/authority and finite attached renewal/cancellation
contracts in `api/v1`, with thin HTTP/client adapters. Validate versions, identities,
capability, generation, expiry and response coherence; a requested attachment with
missing authority fails closed. Ordinary v1 requests/responses retain their shape
when attached mode is absent. Existing disposable renew/cancel behavior is unchanged.
An attached cancellation uses its own fenced request, never the unfenced operator
stop API. The local private administration socket remains the security boundary;
the capability prevents accidental stale-owner mutation, not same-user operators.

Serialize renewal, cancellation, expiry and operator mutations with existing
locks. Renewal cannot revive expired or ended authority. Queue expiry with the
exact owner/attempt and recheck it after obtaining the mutation lock; a queued old
expiry or delayed cancellation must not act on a replacement. Reuse the configured
lease duration and shared ticker/cleanup workers, reading attached deadlines from
the execution owner rather than writing a persistent lease. No new expiry service.

Operator stop, scene stop, restart, reset and destroy end attachment using their
existing serialized semantics and cleanup fences. They need no attachment token.
Replacement does not transfer authority. Once ended, attachment cannot regain
mutation authority, including when operator cleanup fails. Retain ordinary
results and uncertainty; do not invent a durable attachment outcome journal.
Daemon shutdown/recovery retains its existing behavior and never resumes a lease.

### Waiting, output and completion

Use one shared attached client path for both CLI entry points. Pin the accepted
instance, attempt and daemon generation throughout; never re-resolve a scene name
to follow a replacement. Renew while the accepted attempt runs, including startup,
and observe recorded status through the existing finite API. Choose request/poll
budgets compatible with the configured lease, without adding CLI tuning flags.
This is finite request polling for an invocation of unbounded runtime duration,
not a subscription or a new daemon observation worker.

Human output and typed NDJSON identify starting, ready, failure and stopped
observations and include aggregate `LogRecord` output with its original context
and binary encoding. Frame machine records with an explicit type and keep private
authority out of every emitted record. Read the existing unfiltered log cursor,
select only the accepted attempt, and expose existing gaps/warnings. Drain available
pages at completion, subject to existing request/cleanup bounds; no total ordering,
lossless delivery, transition replay or completeness guarantee is added. Standalone
`logs` and status-follow retain their contracts and never renew an attachment.

Use existing startup readiness and runtime failure/liveness detection. Becoming
unready after startup without an existing detected failure introduces no new
health probes. Startup/runtime failure ends attached waiting nonzero after existing
cleanup. A verified external stop of the same attempt exits 0; replacement or
daemon-generation change exits nonzero. A terminal enum alone cannot prove cleanup:
collection/cleanup failures and uncertainty remain nonzero and visible.

SIGINT/SIGTERM request fenced bounded cancellation and join, then exit 130/143
after successful cleanup. Cleanup failure remains a reported nonzero error.
Parent cancellation also requests cleanup and is nonzero. Transport, malformed
response or output failure ends the client nonzero; attempt bounded fenced cleanup
where authority is known, otherwise stop renewal and rely on lease expiry. Never
fall back to unfenced cancellation. Blocked output need not keep renewing forever;
lease expiry may stop the scene when the consumer cannot make progress. A final
record is possible only while output remains writable. No automatic reconnection.

## Consequences

The new maintained state is one attachment on each attached execution owner.
Existing lifecycle writers still record attempts/results/evidence; no new durable
field, writer, state version, index, dependency, retention rule or operator setup
is required. Ordinary persistent and disposable work gains no lease or log polling.
The shared expiry ticker already runs; only attached owners add expiry eligibility
and their clients add renewal/status/log requests. Cancellation workers retain
existing ownership, shutdown budget and join requirements.

Reuse preserves data, credentials, routes/peer isolation and native cleanup limits:
application cleanup may report observed-empty and still miss descendants; known
incomplete/lost authority remains fenced. A stalled client/output can lose its
lease, and log delivery inherits current cursor/retention limitations. Operators
use existing stop/restart and uncertainty-recovery procedures; reset/destroy remain
explicit data-removal choices. Any need for persistent attachment state, broader
writer changes or new background machinery requires a scope review before coding.

## Alternatives considered

- **CLI signal handler plus existing stop.** Less code, but killed clients leave
  runtime indefinitely and delayed ID-only stop can target a replacement.
- **One long HTTP request whose disconnect cancels.** Avoids renewal calls but
  makes transport lifetime the authority and changes the finite-request model;
  finite leases already provide the selected bounded client-loss behavior.
- **Persist attached leases in existing instance lease fields.** Reuses stored
  deadlines, but extends disposable validation, writers and compatibility/recovery
  obligations. Recovery already interrupts execution, so durable authority buys no
  required continuation. Reuse timing/cleanup with ephemeral authority instead.
- **Separate start, then acquire an attachment.** Smaller start response changes,
  but creates unowned-runtime and operator-replacement races between operations.
  Granting authority at serialized execution acceptance closes that gap.
- **Durable events, continuous health checks or reconnectable owners.** Stronger
  guarantees add producers, retention or background work without this workflow's
  need. Existing log pages and sampled recorded state suffice.

## Revisit when

Real attached tasks repeatedly expire under normal log volume or daemon latency;
existing log cursors materially prevent usable aggregate output; or adopters need
runtime continuation across daemon replacement, reattachment, durable transitions
or continuous post-readiness health. Reopen the guarantee and measured scope first.

## References

- [Issue 50](https://github.com/octacian/backlot/issues/50)
- [Daemon-owned lifetimes](20261007T052013565Z-use-explicit-scene-lifetimes-and-daemon-owned-execution.md)
- [Shared typed API](20261007T052013961Z-use-go-with-a-shared-typed-local-api-and-urfave-cli.md)
- [Recorded scene targeting and sampled observation](20261021T000615495Z-resolve-persistent-scenes-from-recorded-identity-and-sample-status.md)
- [Current native supervision and inherited recovery/configuration](20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md)
- [Lifecycle acceptance and cleanup](../../internal/daemon/runtime.go)
- [Existing expiry coordination](../../internal/daemon/service.go)
- [Retained log pages](../../internal/daemon/logs.go)
- [Runtime client and log rendering](../../internal/cli/runtime.go)
- [Local daemon operations](../daemon.md)
- [Results and evidence](../evidence.md)
