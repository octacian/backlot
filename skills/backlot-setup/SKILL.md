---
name: backlot-setup
description: 'Adopt Backlot in an application by adapting existing start, build, migration and test commands, or intentionally redesign its Backlot scenes and configuration. Use for adding or changing Backlot adoption, not ordinary application edits or running already-configured scenes.'
compatibility: Shell access and a Backlot executable with discovery contracts; macOS ARM64 or Linux amd64. Node is only needed for optional npx skill installation.
license: BSD-3-Clause
---

# Set up Backlot

Work from the adopter's project; no Backlot source checkout is required. Read
[compatibility](references/compatibility.md) and the generated
[read contract inventory](references/contracts.json) first. Record the executable path,
version/available commit and selected daemon state directory. Check required installed
capabilities and targeted command help before using the procedures below. Allow
`dev`/`dev-dirty`; users of global development builds must refresh installed skill
copies as their runtime changes. Capability/help presence does not prove semantics.

1. Inspect the application's own instructions and working foreground/build/test
   commands. Preserve its tools and argument vectors. Read
   [adoption](references/adoption.md) to map them to components, scene lifetimes,
   allocated ports, readiness/dependency edges and declared outputs. Ask only for
   consequential missing choices, such as destructive data replacement.
2. Start with the smallest needed native scene; add containers/HTTPS only when the
   application needs them. Use [manifest](references/manifest.md), bundled
   [manifest schema](assets/schemas/manifest-v1.schema.json) and
   [adaptation example](assets/examples/adoption/backlot.yaml). The example is a
   declaration outline, not a runnable app: replace its commands with the actual
   application. Its [seed](assets/examples/adoption/defaults.env) is literal data.
   Declare selected dependencies/resources explicitly, terminal_job for disposable
   tests, and output concurrency groups for generated files. Offline schema success
   does not replace `backlot plan` or actual execution.
3. Discover with `scene list --json` and `scene describe NAME --json`; inspect
   `config show --json`. Configure required inputs through explicit `project setup`
   in a terminal or a private import/stdin, never secret argv or committed values.
   Read [private configuration](references/manifest.md#registered-private-project-configuration)
   before editing registrations: linked worktrees share them. Machine providers and
   absolute tool overrides stay outside the manifest. Missing prerequisites need an
   actionable next step; Backlot never installs dependencies, pulls images or trust.
4. Run `plan NAME --json -- ACTUAL_TEST_ARGS` (omit the tail when unneeded).
   Plan checks caller tools/inputs, not daemon/provider readiness. Before daemon
   operations follow the daemon compatibility check. Explicitly start a missing
   daemon only within the task's authorized scope, then use scoped `doctor` to
   check its startup PATH/tools/providers. See [operations](references/operations.md)
   and conditional [HTTPS](references/https.md)/[platform](references/platforms.md)
   references. Never restart shared infrastructure merely to fix your shell PATH.
5. Prove the actual application flow with `scene start NAME --json` for persistent
   work or `run NAME --json -- ACTUAL_TEST_ARGS` for disposable tests. Preserve the
   process exit and parse the named result as described by [CLI](references/cli.md).
   Record returned instance/attempt IDs and whether persistent work was reused;
   discovery, plan, doctor and metadata-only prepared state do not prove success.
   If a mutation reply is missing, acceptance is unknown: inspect recorded scope
   before considering retry, and never blindly repeat it.
6. Inspect an actual application failure too when validating adoption. Read
   [evidence](references/evidence.md) and [lifecycle](references/daemon.md#identity-and-lifecycle).
   Distinguish terminal exit, runtime, collection and cleanup outcomes; preserve
   uncertainty. Stop/destroy only work authorized for this task, by returned ID in
   the same daemon scope. Stop retains data; reset/destroy need explicit removal
   intent. Reused scenes, peer worktrees, shared servers, daemons and gateways are
   not routine cleanup targets. Status/logs/export only observe; attachment owns a
   new attempt and refuses existing runtime.

Keep private state and selected startup details private: declared persistent startup
summaries may include credentials even in JSON. Secret retrieval is explicit;
ordinary diagnostics should not reveal inputs. Application logs/artifacts can
contain secrets; review before sharing. An optional short project-owned instruction
block can record scene names, normal commands and conventions, never credentials
or a copied manual. Report actual validation, retained work and remaining prerequisites.
