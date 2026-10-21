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

## Guides

| Topic | Guide |
| --- | --- |
| User entry points | [README quickstart](../README.md), [installation and operations](operations.md), [runnable macOS HTTPS example](../examples/macos-quickstart/README.md) |
| Manifest and offline planning | [Manifest](manifest.md) |
| Daemon, native execution and scene targeting | [Local daemon](daemon.md) |
| Containers and private state | [Containers](containers.md) |
| Gateway setup, publication and application trust | [HTTPS](https.md) |
| Exits, logs, artifacts, fixtures and retention | [Results and evidence](evidence.md) |
| macOS/Linux targets and WSL configuration | [Platforms](platforms.md) |
| Planned native Windows work (#34/#37/#38) | [Design and acceptance](windows-design.md) |
| GitHub Actions and Chromium scenes | [CI quickstart](ci.md), [Ubuntu example](../examples/ci-scenes/README.md) |
| Release gates and external evidence | [Verification](release-acceptance/README.md), [four-workflow fixture](../examples/release-acceptance/README.md) |

[Testing and tooling](testing.md#commands) is the contributor command reference.
