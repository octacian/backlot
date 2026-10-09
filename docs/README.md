# Developer documentation

These guides are shared by human contributors and coding agents. Keep each rule
in one authoritative guide and link to it from indexes and entry points.

## Required reading

Before planning or editing, read:

- [Contribution workflow](contributing.md).
- [Go practices](go-practices.md).
- [Testing and tooling](testing.md).
- [Architecture decisions](adr/README.md), then relevant records.

## By task

| Task | Read |
| --- | --- |
| Product or runtime implementation | [V1 PRD](prd.md), [architecture](architecture.md), [implementation plan](implementation-plan.md) |
| CLI or API contracts | [Architecture: shared API](architecture.md#shared-api), [Go practices](go-practices.md) |
| Commits and pull requests | [Git workflow](git-workflow.md) |
| ADR creation or changes | [ADR guide](adr/README.md), [template](adr/template.md) |

The [local daemon guide](daemon.md) documents preparation and native execution.
The [container/private-state guide](containers.md) documents milestone 4.
The [HTTPS/mixed execution guide](https.md) documents configured gateway setup,
publication ownership and milestone 5.

The [disposable results and evidence guide](evidence.md) documents terminal exits,
artifacts, fixtures, cancellation, kept resources and daemon-wide retention.

The [manifest and offline planning guide](manifest.md) documents milestone 1.

The command reference lives in [Testing and tooling](testing.md). README and
agent entry points link there rather than maintaining competing command lists.
