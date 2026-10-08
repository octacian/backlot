# Backlot

Backlot is a planned local development and testing stack manager for humans and
coding agents. A background daemon coordinates native processes, containers,
isolated resources, and HTTPS routes from a project's declarative scenes.

The intended interface is a single command from any checkout or Git worktree:

```sh
backlot run dev
backlot run preview
backlot run db-integration -- <test arguments>
backlot run ui-test -- <test arguments>
```

Scene names are project-defined. Persistent environments return a usable URL;
disposable runs return their outcome and retained evidence.

**Status:** offline planning and local durable metadata preparation are implemented.
The executable provides help,
`backlot version [--json]`, and `backlot plan <scene> [--project PATH]
[--config PATH] [--json] -- <terminal-job args>`. Planning validates contracts,
files, inputs and native executables without running work or allocating resources.
See the [manifest guide](docs/manifest.md) and generic fixtures.
Explicit daemon control and metadata-only `prepare`, `inspect`, `cancel`, `renew`
and local `doctor` are described in the [daemon guide](docs/daemon.md). Prepared
metadata never claims execution or readiness; the scene execution commands above
remain V1 requirements. The initial release target is macOS
ARM64; Linux and native Windows support are architectural targets for later.

## Development

See [Developer documentation](docs/README.md) for setup, commands, and shared
contributor expectations. Begin with the [V1 PRD](docs/prd.md) and
[implementation plan](docs/implementation-plan.md) for product work.

## License

Backlot is BSD 3-Clause licensed. See [LICENSE](LICENSE).
