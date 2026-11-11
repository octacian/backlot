# Contributor entry point

Read [Developer documentation](docs/README.md) before planning or editing. Its
guides are the canonical engineering expectations for humans and coding agents.
Read the required guides and the task-specific guides linked there.

Inspect directories you will edit for nested `AGENTS.md` files (none currently).
More specific instructions apply within their scope. Explicit user instructions
and runtime instructions take precedence over repository guidance.

Keep this file a reading map. Maintain shared expectations in the guides rather
than duplicating them in agent-specific configuration.

## Commands and documentation

- Check: `make check` (includes `bash .github/scripts/todo-check.sh`).
- Generate: `make schema-generate skills-generate notices`.
- Documentation: `go run go.auvem.com/corestack/cmd/corestack-docs@v0.2.0 check`.
- Tier: ADRs only; use issues/epics for delivery scope and PRs for execution evidence.
- Content placement: ADRs hold decisions, reasons, options and consequences;
  plans hold scope, steps and validation; PRs hold execution evidence and history.
- Update only documentation the change makes wrong; do not add coverage nobody needs.
- Before final review and merge, reconcile affected records against the latest
  target branch plus the change under [ADR policy](/docs/adr/README.md).
- Optional workflow aids: feature-delivery, write-adr, review-adr, review-pr,
  rereview-pr, resolve-pr-conflicts and squash-commit-message. Installation is not required.

## Comments

- Document package purpose, ownership, boundaries and public contracts with native
  doc comments; document generated surfaces at their source.
- Explain why at invariants, authorization, concurrency, destructive edges and
  deliberate deviations; avoid narration, changelog comments and commented-out code.
- Use `invariant:`, `authz:` and `shortcut: <limit>, <when to upgrade>` where useful;
  retain Go `Deprecated:`. Every TODO needs an issue URL.
