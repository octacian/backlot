<!-- Generated from docs/adoption.md; run make skills-generate. -->

# Adopt Backlot in an existing application

Keep the commands that already work. Backlot supplies lifecycle, allocated values,
readiness, isolation and evidence; your tools still build, migrate, serve and test.
Install Backlot using the [README](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/README.md#install-backlot) and check the
[platform limits](platforms.md). This path starts with native unpublished work;
use the short [HTTPS](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/README.md#enable-local-https) and
[container](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/README.md#enable-containers) paths when your application needs them.

## Translate existing commands

First run your application's ordinary commands yourself and identify their inputs:

| Existing command or setting | Backlot declaration |
| --- | --- |
| Foreground server or watcher | Native `service` with `command.tool` and an argument array |
| Build or migration that exits | `job` with `policy: each-start`; depend on its successful completion |
| Initialization of new owned data only | `job` with `policy: fresh-only` and `initializes` naming its data resources |
| Test command whose exit decides the result | Disposable scene's selected each-start `terminal_job` |
| Listener port | `port` resource, service `ports`, and explicit environment assignment |
| Required service/preparation | Select it in the scene and declare `depends_on` with `ready`/`completed` |
| Environment and credentials | Explicit mappings/required keys; declare secret inputs and register values privately |
| Reports or generated build directory | Project-relative `output` with a concurrency group; reports also declare `artifacts` |

Commands are direct executable/argument vectors, without shell expansion. For
`npm run dev`, declare tool `npm` and args `[run, dev]`; append arguments using npm's
own conventions. Keep the process in the foreground. Ordinary Air/npm watchers
may create groups inside the supervised session; daemonization, `setsid`, privilege
changes and delegation to unrelated supervisors are unsupported. See the
[native contract](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/daemon.md#native-execution) and the existing
[watcher example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/native-watchers/README.md).

## Start with a port and one service

Create `backlot.yaml` at the application root. This illustration adapts existing
npm scripts; replace the scripts/tool with your own working commands. The `dev`
script must read `PORT` and bind a loopback-reachable listener. It must not pick
another port when the assigned one is busy.

```yaml
version: backlot/v1
project: my-app
tools: {npm: npm}
resources:
  http: {kind: port}
components:
  build:
    kind: job
    runtime: native
    policy: each-start
    command: {tool: npm, args: [run, build]}
  app:
    kind: service
    runtime: native
    command: {tool: npm, args: [run, dev]}
    depends_on: [{component: build, condition: completed}]
    resources: [http]
    ports: {http: {resource: http}}
    environment:
      assign:
        PORT: {ref: {kind: resource, name: http, field: port}}
    readiness:
      kind: tcp
      target: {ref: {kind: service, name: app, field: port, port: http}}
      timeout: 30s
  tests:
    kind: job
    runtime: native
    policy: each-start
    command: {tool: npm, args: [test]}
    depends_on: [{component: app, condition: ready}]
    environment:
      assign:
        APP_PORT: {ref: {kind: service, name: app, field: port, port: http}}
scenes:
  dev:
    lifetime: persistent
    components: [build, app]
    resources: [http]
  test:
    lifetime: disposable
    components: [build, app, tests]
    resources: [http]
    terminal_job: tests
```

The test script must consume `APP_PORT` (and loopback host) to address that service;
Backlot does not invent application environment names or concatenate URLs.
If no build is needed, remove that component and its dependency from both scenes.
If commands write `dist` or reports, declare their outputs/groups before concurrent
use; [the existing adoption manifest](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/adoption/backlot.yaml) shows
build outputs. Backlot cannot lock undeclared writes. Each selected dependency
must be listed in the scene; a reference alone does not select or wait for it.

A TCP probe observes a listener, not schema or credential correctness. Use a
native command readiness probe when the application needs an authenticated query
or other application-level check. The [Go/MariaDB recipe](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/native-go-mariadb/README.md)
provides one; run migrations as your own preparation job after database readiness,
then make the application/tests depend on its successful completion. For a
fresh-only job, `initializes` must name attached directory/volume/allocation
resources. Backlot records initialization only after success; it does not roll
back partially applied application migrations.

## Supply configuration privately

Native work inherits only the documented launch baseline, not your entire shell.
Use explicit `pass_through`, literal `KEY=value` seed files or assignments;
see [environment layering](manifest.md#values-and-environment). A long-lived daemon
uses its own startup environment for inherited values and tool PATH lookup.
A successful caller-side plan does not prove the daemon sees the same tools.

Declare named inputs in the manifest and reference them from environment mappings:

```yaml
inputs:
  api_token: {secret: true, description: Local application API credential}
# Within the consuming component's environment.assign:
# API_TOKEN: {ref: {kind: input, name: api_token}}
```

Run `backlot project setup` in a terminal to enter them. Private inputs and local
tool overrides are registered outside the checkout and reused by linked worktrees;
independent clones need their own setup. For automation use a private file/stdin,
not credential argv. Keep secrets and absolute machine-specific paths out of the
committed project manifest. Provider endpoints and shared tool settings go in the
external machine file; it cannot supply input values. See
[private setup and provenance](manifest.md#registered-private-project-configuration).

## Plan, execute and retain evidence

```sh
backlot scene list --json
backlot scene describe test --json
backlot config show --json
backlot plan test --json
backlot daemon start --json
backlot doctor --scene test --json
backlot scene start dev --json
backlot run test --json -- APPLICATION_TEST_ARGS
```

Replace the final placeholder with your runner's real arguments, or omit it.
Only the terminal job gets that tail. Read each command's exit and result before
continuing; discovery/configuration/plan/doctor do not establish execution success.
Once ready, use the [daily loop](operations.md#daily-loop) for logs, evidence and
scoped cleanup, and the [lifecycle table](daemon.md#identity-and-lifecycle) when edits
need restart/reset. Test failures keep their original exit independently of
orchestration, collection and cleanup.

Use the existing [loopback Playwright recipe](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/native-playwright/README.md)
for direct test arguments and collected reports, or
[Go tests with disposable MariaDB](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/native-go-mariadb/README.md) for
private data and environment-only credentials. They contain runnable application
code; the manifest above is an adaptation outline. Add
[HTTPS publication/application trust](https.md) or [containers/state](containers.md)
when required, keeping those topology-specific settings explicit.
