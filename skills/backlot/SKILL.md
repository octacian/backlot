---
name: backlot
description: 'Operate an already-configured Backlot project: discover scenes, run tests or development services, apply compatible manifest drift, inspect failures and evidence, and clean up task-owned work. Use for daily Backlot operation, not initial adoption or redesigning project orchestration.'
compatibility: Shell access and a Backlot executable with discovery contracts; macOS ARM64 or Linux amd64. Node is only needed for optional npx skill installation.
license: BSD-3-Clause
---

# Use Backlot daily

Read [compatibility](references/compatibility.md) and the generated
[read contract inventory](references/contracts.json) first. Record the executable path,
version/available commit and daemon state directory; check installed capabilities
and the help for commands/flags you will use. `dev`/`dev-dirty` are allowed. Users
of global development builds must refresh installed skills with their runtime;
capability/help presence is not semantic or provider-readiness proof.

1. From the checkout, read its instructions and discover `scene list --json`,
   `scene describe NAME --json`, then `config show --json`. Use declared names and
   the project's existing tests. Do not rewrite adoption just to run them. If no
   manifest exists, explain that setup is needed; this skill does not require the
   separate setup skill to be installed. Read [operations](references/operations.md)
   for missing inputs/tools and caller-versus-daemon selection.
2. Plan the selected scene and exact terminal argument vector:
   `backlot plan NAME --json -- TEST_ARGS`. Omit the tail for persistent work.
   Check daemon capabilities before daemon operations; explicitly start a missing
   daemon only when authorized. `doctor --scene NAME --json` checks the selected
   daemon context/providers; deferred/unsupported checks do not establish readiness.
   Missing tools/inputs/images/trust need targeted setup, not blanket installation
   or automatic shared-daemon replacement. Private project setup changes are shared
   across linked worktrees; see [manifest](references/manifest.md).
3. Use `run NAME --json -- TEST_ARGS` for disposable tests, `scene start NAME --json`
   for persistent readiness-return use, or `scene start --attached NAME --json`
   only when asked to own one new persistent attempt until client end. Attachment
   refuses existing runtime; status/follow/log/export never attach or renew leases.
   Capture original stdout/stderr/exit independently and record returned IDs:
   ordinary run uses `instance`, named startup nests it under `result.instance`.
   A repeated ordinary start may reuse existing work; returned identity alone does
   not make that work task-owned. See [CLI results](references/cli.md#interpreting-machine-results).
4. Check the original test exit separately from orchestration, collection, cleanup,
   kept resources and any last-observed error. Status exit 0 can describe a failed
   execution. A missing mutation reply means unknown acceptance, not rejection or
   permission to retry. Investigate recorded identity/scope; keep uncertainty visible.
5. For edits, consult [identity/lifecycle](references/daemon.md#identity-and-lifecycle)
   and [drift](references/containers.md#generations-jobs-and-drift). Repeated start
   joins starting/ready work with unapplied drift, or requests one compatible new
   attempt after eligible stopped/failed work and cleanup. New starts use current
   explicit/default config; `restart ID` deliberately replaces healthy work.
   On `startup_conflict`, inspect the bound attempt's retained history/logs/export;
   never retry a missing mutation reply automatically.
   Protected drift needs a deliberate reset/destroy data-removal decision. Do not
   destroy retained data or stop a reused/peer execution to force your change.
6. Bound diagnostics: `history ID --limit 100 --json`, `inspect ID --last-completed --json`,
   exact `inspect ID --attempt ATTEMPT --json`, `status ID --json`, `logs ID --page --limit 100 --json`,
   `fixtures ID --json`, or `doctor --instance ID --json`. Preserve page filters
   and continuation, and bound follow by duration/record limit. Read
   [evidence](references/evidence.md) for partial capture and export. A kept failure
   retains healthy dependencies; stop its authorized runtime before export to a new
   destination under a private parent, outside daemon state. Keep the original
   failure and evidence; do not claim successful tests from successful export.
7. End only authorized task-owned work in the same daemon scope, using the recorded
   ID: stop preserves data/history; destroy removes owned data after stop and erases
   attempts/logs/artifacts. Export needed exact-attempt evidence before destroy;
   caller-owned bundles survive. History prune removes selected evidence, never data.
   Preserve uncertain resources/journals and report residuals. Do not prune by name,
   hostname or observed PID, stop peer worktrees, or routinely stop shared daemons,
   gateways or servers. Cancellation of observation does not stop runtime.

Private imports/stdin supply secrets, never argv or committed project instructions.
Explicit secret retrieval is separate from ordinary diagnostics. Declared startup
summaries may disclose credentials even in JSON; application logs/artifacts may
also contain secrets. Protect/review evidence before sharing. Load
[HTTPS](references/https.md) and [platforms](references/platforms.md) only for the
selected topology. Report test outcome, independent failures, retained work and
cleanup uncertainty without equating discovery, planning or doctor with execution.
