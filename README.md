# Backlot

Backlot is a local development and testing stack manager for humans and
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

**Status:** native/container execution, private retained state, configured HTTPS
publication and disposable results/evidence are implemented. Release readiness
requires independent reviews, frozen-candidate verification and published-HEAD CI;
this status is not a V1 release declaration. See the
[release verification guide](docs/release-acceptance/README.md).

Start with [installation and operations](docs/operations.md) and the
[runnable four-workflow fixture](examples/release-acceptance/README.md). The
[manifest guide](docs/manifest.md) defines strict offline planning/configuration;
the [daemon guide](docs/daemon.md), [private-state guide](docs/containers.md),
[HTTPS guide](docs/https.md) and [evidence guide](docs/evidence.md) describe runtime
contracts. Use `list` to discover recorded IDs and `status`/`inspect` to inspect
them; ordinary views redact secrets and ownership authority. Prepared metadata
never claims execution/readiness.

Native commands and all descendants must stay in their supervised process group;
detachment and external process delegation are unsupported. The release target is
macOS ARM64; Linux and Windows compile checks are portability evidence only.

## Development

See [Developer documentation](docs/README.md) for setup, commands, and shared
contributor expectations. Begin with the [V1 PRD](docs/prd.md) and
[implementation plan](docs/implementation-plan.md) for product work.

## License

Backlot is BSD 3-Clause licensed. See [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY_NOTICES.md).
