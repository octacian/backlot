# Backlot

Backlot runs isolated development and test environments from a project's
`backlot.yaml`: native processes, optional containers, private resources and HTTPS
routes. Each Git worktree gets its own instance and allocated ports; published
scenes also get a canonical origin.

## Install Backlot

Install with **Go 1.27.1 or newer**. Backlot targets **macOS ARM64**, **Ubuntu 24.04 amd64** and **Debian 13.4 amd64**; see the [platform policy](docs/platforms.md) for runtime gates and Linux prerequisites:

```sh
go install github.com/octacian/backlot/cmd/backlot@latest
backlot version --json
```

If `backlot` is not found, add the directory reported by `go env GOBIN` to PATH
when set; otherwise add `bin` under the first directory in `go env GOPATH`
(default `$HOME/go/bin`).
Save any PATH change in your shell profile. Git is needed for project worktrees;
Make is needed only for the alternative source build.

[Release installer, upgrades, manual archives and reproducible source builds](docs/operations.md).
The shell installer needs a helper-bearing published release; before the first
such release is published, use Go or manual archives.

## Install agent skills

For shell coding agents, with Node >=22.20.0 and Git, run from your project:

```sh
npx skills add octacian/backlot --skill backlot-setup --skill backlot
```

This installs setup and daily-use guidance without a Backlot source checkout.
See [agent selection, global installation, updates and compatibility](docs/agent-skills.md).

## Run an existing configured project

Install the application's dependencies using its own instructions. From its
checkout, discover the project-defined scenes before configuring tools or inputs:

```sh
backlot scene list --json
backlot scene describe dev --json
```

Replace `dev` with the project's persistent development scene. If it declares
private inputs, run `backlot project setup` once in a terminal. Configure HTTPS or
Docker below when the scene requires them, then:

```sh
backlot daemon start --json
backlot plan dev --json
backlot scene start dev --json
```

Start returns at readiness. Record `result.instance.id`; for published scenes,
open `result.instance.execution.origin`. Closing the terminal leaves ordinary
persistent starts running. Stop runtime while retaining development data with:

```sh
backlot scene stop dev --json
```

Repeat start to resume. After a daemon or machine restart, explicitly start the
daemon and any required managed gateway again. For disposable tests, use
`backlot run test --json -- APPLICATION_TEST_ARGS`; scene names and arguments
come from the project. See the [daily loop and failure recovery](docs/operations.md#daily-loop).

## Enable local HTTPS

Install stock Caddy separately (`brew install caddy` with Homebrew). Put this
machine configuration **outside the checkout**, in
`~/Library/Application Support/backlot/config.yaml` on macOS or
`${XDG_CONFIG_HOME:-$HOME/.config}/backlot/config.yaml` on Linux. Use your actual
absolute `command -v caddy` path and free unprivileged ports:

```yaml
version: backlot/v1
caddy:
  mode: managed
  executable: /absolute/path/to/caddy
  endpoint: http://127.0.0.1:2020
  https_port: 8443
```

```sh
backlot daemon start --json
backlot gateway start --json
/absolute/path/to/caddy trust --address 127.0.0.1:2020 --ca local
backlot scene start dev --json
```

Run stock `caddy trust` yourself against **your owned gateway**; it may request
administrator authorization. Backlot never installs system/browser trust.
Applications and some browsers need separate explicit public-root settings.
If the daemon attempted TLS before trust installation, stop/start it and start
the gateway again to refresh cached roots. The CA survives this restart.
Follow [HTTPS and application trust](docs/https.md#managed-local-https) and
[Linux resolver/listener setup](docs/platforms.md#linux-https-and-bridge-consumers).
The [runnable macOS HTTPS example](examples/macos-quickstart/README.md) includes
frontend/API/SSR code and tests. Native unpublished scenes need no gateway.

## Enable containers

Install a supported local Docker engine and explicitly pull/build the project's
images with Docker tooling. Backlot never pulls implicitly. Add the engine's
actual Unix socket to your private machine configuration; this can coexist with
`caddy` above:

```yaml
version: backlot/v1
docker:
  endpoint: unix:///var/run/docker.sock
```

Docker Desktop may use another socket; inspect yours with
`docker context inspect --format '{{.Endpoints.docker.Host}}'`. Then run the same
plan/start commands. Try [native Go tests with disposable MariaDB](examples/native-go-mariadb/README.md)
or [shared MariaDB](examples/shared-mariadb/README.md) for separate worktree
databases on one retained server. See [containers and cleanup](docs/containers.md)
and [container HTTPS](docs/https.md#public-root-file-mounts-for-containers) for
explicit bridge reachability and application trust.

<a id="set-up-a-project"></a>

## Adopt Backlot in your project

[Adapt your existing start, build, migration and test commands](docs/adoption.md),
then use the [CLI reference](docs/cli.md), [manifest reference](docs/manifest.md)
and [results/evidence guide](docs/evidence.md). For a small native browser recipe,
start with [Playwright over loopback HTTP](examples/native-playwright/README.md).
For GitHub Actions, use the [CI quickstart](docs/ci.md).

Native application work must stay within its supervised session; new sessions,
privilege changes and unrelated supervisors are unsupported. See the
[platform limits](docs/platforms.md). [Contributor documentation](docs/README.md).

## License

Backlot is [BSD 3-Clause licensed](LICENSE).
