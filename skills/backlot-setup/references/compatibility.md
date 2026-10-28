<!-- Generated from docs/agent-skills.md; run make skills-generate. -->

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
exact commit/release allowlist. Capability/help presence does not prove matching
semantics or provider readiness. Users of global development builds are responsible
for keeping installed skills current as they develop; refresh skills together
with runtime changes. Unexpected behavior still requires diagnosis, not retries.

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
