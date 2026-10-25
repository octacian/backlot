# Developer documentation

These guides are shared by human contributors and coding agents. Keep each rule
in one authoritative guide and link to it from indexes and entry points.

## Required reading

Before planning or editing, read:

- [Contribution workflow](contributing.md).
- [Go practices](go-practices.md).
- Testing and tooling: [Setup](testing.md#setup), [Commands](testing.md#commands)
  and [CI and platform evidence](testing.md#ci-and-platform-evidence), including
  uncached runtime tests and crash-fixture ownership/cleanup safety.
- [Architecture decisions](adr/README.md), then relevant records.

## By task

| Task | Read |
| --- | --- |
| Product or runtime implementation | Relevant sections of the [V1 PRD](prd.md) and [architecture](architecture.md), applicable [ADRs](adr/README.md), and relevant integration sections in [Testing and tooling](testing.md) |
| CLI or API contracts | [Architecture: shared API](architecture.md#shared-api), [Go practices](go-practices.md) |
| Commits and pull requests | [Git workflow](git-workflow.md) |
| Delivery scope or status | [Implementation plan](implementation-plan.md) and linked issues/PRs |
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
| Preparing a release | [Release procedure](preparing-a-release.md) |
| Targeted runtime diagnostics | [Diagnostics and evidence](release-acceptance/README.md), [four-workflow fixture](../examples/release-acceptance/README.md) |

[Testing and tooling](testing.md#commands) is the contributor command reference.
