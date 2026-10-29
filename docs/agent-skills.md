# Install Backlot agent skills

`backlot-setup` adapts an application's existing commands and Backlot declarations;
`backlot` operates configured projects, tests, drift, diagnostics and task cleanup.
Each works independently with local references, schemas and adaptation examples,
without a Backlot source checkout. Both teach the existing CLI.

## Installation and updates

With Node >=22.20.0 and Git, run from your project:

```sh
npx skills add octacian/backlot --skill backlot-setup --skill backlot
```

The optional [skills installer](https://github.com/vercel-labs/skills) was verified
with skills 1.7.4; Node is not a Backlot runtime or repository-check prerequisite.
Choose your supported agent when prompted; `--agent codex` selects Codex explicitly.
Select just one skill by omitting the other `--skill`. Project installation is the
default; append `--global` for user-wide availability. Review the destination and
existing installation before replacing it. The installer owns its files, links
and lock metadata; it does not install Backlot, Docker, Caddy or application tools.

For reviewed local development use an absolute source directory:
`npx skills add /path/to/backlot --skill backlot --agent codex`.
For a reproducible remote source, use
`npx skills add 'https://github.com/octacian/backlot/tree/REF' --skill backlot`,
replacing `REF` with the reviewed immutable commit or published tag containing
these skills. Do not assume an older release contains them. Keep the skill source
and CLI pairing in your task evidence; pinned installation alone does not prove
compatibility with a different executable.

For remote moving sources, `npx skills check` and `npx skills update backlot-setup backlot`
check/update installed copies. Local-source development installs require explicit
reinstallation from the updated local source; remote updates do not synchronize
a local checkout. For pinned copies reinstall the selected immutable source.
Recheck compatibility after refreshing; nothing updates automatically.

## Compatibility

Before using either procedure, run `command -v backlot`, `backlot version --json`
and `backlot contracts --json`. Record the selected path, reported version and
available commit. Compare API/state and required read IDs/modes using the
[read-contract reference](cli.md#installed-read-contracts-and-schemas):
scene-declarations, configuration, instance-filter, log-page, logs and export.
Each installed skill also supplies a generated `references/contracts.json`
inventory from the shared API; it describes reads, not a complete command or
mutation catalog.

Before a mutation, inspect that installed command's `--help` for its target and
flags (for example `backlot run --help` or `backlot scene start --help`). Accept
`dev` and `dev-dirty` builds after required capabilities/help checks; there is no
exact commit/release allowlist. These checks do not prove semantics or provider
readiness. Users of global development builds must refresh installed skills with
runtime changes. Diagnose unexpected behavior before considering retries.

Before daemon operations run `backlot contracts --daemon --json`, including the
same `--state-dir PATH` used for the task when nondefault. Require matching API/state
and the needed finite daemon reads: instance-filter, configuration, log-page, logs
and export. Unlike local `logs/v1` NDJSON, the daemon capability reports finite log
reads. Daemon contracts include generation but no executable build identity;
do not attribute the caller's version to it. A missing daemon needs explicit
startup within authorized scope, then this check; do not silently change daemon scope.

If the executable/daemon lacks required capabilities, recognized output or command
flags, stop that dependent operation and report observed identity and the missing
requirement. Check PATH for an older binary. Refresh the installed skill from the
matching development source or reviewed ref, or explicitly install an appropriate
Backlot executable using the [installation guide](operations.md). Prefer CLI and
daemon from the same installation. Replacing a shared daemon is deliberate
maintenance: coordinate existing users, stop with its old matching binary, retain
state and CA, then explicitly start the intended binary in the same scope. Never
auto-upgrade, delete state to bypass compatibility, or downgrade into newer state.
When binary maintenance is authorized, use the installation guide's supported
release installer or matching manual procedure; skill refresh and project pins
remain separate. A running daemon still requires the installer terminal consent;
noninteractive agents must report that requirement rather than synthesize consent.

## Bundle maintenance

See [testing](testing.md#agent-skills) for generation, validation and evaluation. The
[decision](adr/20261028T062335915Z-ship-self-contained-setup-and-daily-backlot-skills.md)
records the packaging and compatibility boundary.
