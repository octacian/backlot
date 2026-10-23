# Backlot

Backlot runs isolated development and test environments from a project's
`backlot.yaml`: native processes, optional containers, private resources and HTTPS
routes. Each Git worktree gets its own instance, ports and canonical origin.

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

[Installation details and reproducible source builds](docs/operations.md).

## Run an existing configured project

Prepare this machine **once** for local HTTPS. Install stock Caddy separately
(`brew install caddy` if you use Homebrew). Save this private configuration outside
your project; replace the executable with the absolute `command -v caddy` path:

```sh
umask 077
case "$(uname -s)" in
  Darwin) backlot_config_dir="$HOME/Library/Application Support/backlot" ;;
  Linux) backlot_config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/backlot" ;;
esac
mkdir -p "$backlot_config_dir"
cat > "$backlot_config_dir/config.yaml" <<'YAML'
version: backlot/v1
caddy:
  mode: managed
  executable: /opt/homebrew/bin/caddy
  endpoint: http://127.0.0.1:2020
  https_port: 8443
YAML
backlot daemon start --json
backlot gateway start --json
/opt/homebrew/bin/caddy trust --address 127.0.0.1:2020 --ca local
```

The default machine file is `~/Library/Application Support/backlot/config.yaml`
on macOS and `${XDG_CONFIG_HOME:-$HOME/.config}/backlot/config.yaml` on Linux.
Daemon state defaults to `daemon/` in that same Backlot directory. Use `--config`
and `--state-dir` to select other paths. First gateway start/restart loads the
default; later commands with no `--config` reuse retained gateway settings even if
the file changes or disappears. An explicit file must match those retained settings.

Use free ports 2020/8443; if occupied, select other unprivileged ports in the file
and trust command. Run the stock trust command yourself against **your owned
gateway**; it may ask for administrator authorization. Backlot never changes
system/browser trust. If your browser uses a separate store, import the public
`$backlot_config_dir/daemon/gateway/data/pki/authorities/local/root.crt` as a trusted authority.
If you tried HTTPS before installing trust, stop/start the daemon and start the
gateway again to refresh cached roots. [Trust and recovery](examples/macos-quickstart/README.md#prepare-once).

Install your project's declared application dependencies using its own setup
instructions. From that project's checkout, daily use is:

```sh
backlot scene start dev --json
```

Open `result.instance.execution.origin` from the result:
`https://bl-<instance-label>.backlot.localhost:8443`. Record `result.instance.id`.
The command returns when ready; closing your terminal leaves the persistent scene
running. Scene names are project-defined; use the project's development scene if
it is not named `dev`. Application trust must already be configured in its manifest.
No public domain, Certbot, router DNS or resolver file is needed.

```sh
backlot scene stop --json dev
```

Repeat `scene start dev` to resume with the same logical origin. After a daemon stop or
machine restart, repeat daemon start and gateway start before running scenes;
retained state preserves the CA. Native-only unpublished scenes need no gateway;
Docker is needed only for container scenes.

## Set up a project

Follow the [runnable macOS HTTPS quickstart](examples/macos-quickstart/README.md)
for a minimal working manifest, included frontend/API/native SSR code, explicit
Node CA settings and disposable tests with retained reports. It also walks through
two concurrent Git worktrees, CA persistence and scoped cleanup. Node/npm are
example application dependencies, not Backlot contributor requirements.

For more: [manifest reference](docs/manifest.md), [HTTPS and application trust](docs/https.md),
[logs and evidence](docs/evidence.md), [operations and diagnosis](docs/operations.md),
and the advanced [four-workflow/container fixture](examples/release-acceptance/README.md).
For separate worktree databases on one retained server, try the minimal
[shared MariaDB example](examples/shared-mariadb/README.md).
Use `backlot list --json` to discover recorded IDs,
and `backlot status --json INSTANCE_ID` to inspect one.
Add `--follow` to observe it, or use `scene status dev --follow` to watch the named
scene through recreation. See [targeting and follow](docs/daemon.md#named-persistent-scenes).

Native commands and descendants must stay in their supervised process group;
detachment and delegation to another supervisor are unsupported. See the
[platform guide](docs/platforms.md) for supported topologies, WSL configuration and
planned Windows work, and [release verification](docs/release-acceptance/README.md)
for broader gates. This is not a V1 release declaration.

For GitHub Actions, follow the [CI quickstart](docs/ci.md) and minimal two-shard
[adopter workflow](examples/ci-scenes/adopter.yml).

## Development

See [Developer documentation](docs/README.md) for contributor setup and checks.

## License

Backlot is [BSD 3-Clause licensed](LICENSE).

For project-specific credentials with shared machine providers, run
`backlot project setup --project /path/to/project` once; ordinary scene commands
automatically reuse the private layer across linked worktrees. See
[private project setup](docs/manifest.md#registered-private-project-configuration).
