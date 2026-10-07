# Git workflow

Use Conventional Commits for commit subjects and PR titles:
`type(scope): short imperative summary`. Scope is optional. Use standard types
such as `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, and `chore`.

Use `type/short-kebab-description` task branches. Do not use an agent or tool name
as the branch prefix. Keep changes focused; inspect the staged diff and exclude
secrets, local machine configuration, binaries, and unrelated work.

Before committing, run the checks required by [Testing and tooling](testing.md).
Describe PRs in terms of the problem, resulting behavior, relevant validation,
and remaining limitations. Keep durable choices in ADRs and implementation
tracking in the implementation plan or issues.

Do not rewrite shared history, delete remote branches, or publish releases
without authorization. Release automation and distribution beyond local builds
will be specified when implemented.
