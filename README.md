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
mkdir -p "$HOME/.config/backlot"
cat > "$HOME/.config/backlot/machine.yaml" <<'YAML'
version: backlot/v1
caddy:
  mode: managed
  executable: /opt/homebrew/bin/caddy
  endpoint: http://127.0.0.1:2020
  https_port: 8443
YAML
backlot daemon start --state-dir "$HOME/.backlot" --json
backlot gateway start --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
/opt/homebrew/bin/caddy trust --address 127.0.0.1:2020 --ca local
```

Use free ports 2020/8443; if occupied, select other unprivileged ports in the file
and trust command. Run the stock trust command yourself against **your owned
gateway**; it may ask for administrator authorization. Backlot never changes
system/browser trust. If your browser uses a separate store, import the public
`$HOME/.backlot/gateway/data/pki/authorities/local/root.crt` as a trusted authority.
If you tried HTTPS before installing trust, stop/start the daemon and start the
gateway again to refresh cached roots. [Trust and recovery](examples/macos-quickstart/README.md#prepare-once).

Install your project's declared application dependencies using its own setup
instructions. From that project's checkout, daily use is:

```sh
backlot run dev --state-dir "$HOME/.backlot" --config "$HOME/.config/backlot/machine.yaml" --json
```

Open `instance.execution.origin` from the result:
`https://bl-<instance-label>.backlot.localhost:8443`. Record `instance.id`.
The command returns when ready; closing your terminal leaves the persistent scene
running. Scene names are project-defined; use the project's development scene if
it is not named `dev`. Application trust must already be configured in its manifest.
No public domain, Certbot, router DNS or resolver file is needed.

```sh
backlot stop --state-dir "$HOME/.backlot" --json INSTANCE_ID
```

Repeat `run dev` to resume with the same logical origin. After a daemon stop or
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
Use `backlot list --state-dir "$HOME/.backlot" --json` to discover recorded IDs,
and `backlot status --state-dir "$HOME/.backlot" --json INSTANCE_ID` to inspect one.

Native commands and descendants must stay in their supervised process group;
detachment and delegation to another supervisor are unsupported. The support
boundary includes the bounded Linux rootful bridge topology in the
[platform guide](docs/platforms.md). WSL has configuration guidance only, without
WSL QA or a guarantee. Native Windows is planned under independent epic #34;
[the Windows design and acceptance plan](docs/windows-design.md) establishes no
Windows runtime support yet. This is not a V1 release declaration; the
[release verification guide](docs/release-acceptance/README.md) records broader gates.

## Development

See [Developer documentation](docs/README.md) for contributor setup and checks.

## License

Backlot is [BSD 3-Clause licensed](LICENSE).
