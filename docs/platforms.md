# Platform support and Linux setup

Backlot targets macOS ARM64 and Linux amd64. The initial Linux targets are
Ubuntu 24.04 amd64 and Debian 13.4 amd64, with a local rootful Docker Engine and
IPv4 bridge containers. Ubuntu has a fresh-runner runtime CI gate; Debian
actual-machine results remain distinct evidence, without a mandatory per-release
rerun. Select platform-specific manual checks for changed guarantees lacking CI
coverage under the [release policy](preparing-a-release.md). Reports must identify
the tested revision, dependency versions, failures/skips and cleanup; a target in
this policy is not evidence that an untested release passed. Other distributions,
architectures, rootless/remote engines and alternative container runtimes are
experimental. Native Windows is a planned expansion under independent epic #34,
not a supported target yet; see the [accepted Windows design](windows-design.md). WSL
guidance below is configuration guidance only, with no WSL QA or support guarantee.

macOS and Linux use permissioned Unix sockets and authenticated guardians.
Application native work may create process groups inside its guardian's session,
with bounded identity-safe cleanup and explicit observed-empty confidence. New
sessions, privilege changes, hostile workloads and unrelated supervisors remain
unsupported. Managed Caddy/system helpers retain strict process groups. See the
[session supervision contract](adr/20261021T002507834Z-allow-cooperative-native-process-groups-within-an-owned-session.md) for missed-process risk and guardian-loss
uncertainty. Host-only selected scenes remain independent of Docker, and offline
or container-only work does not scan processes or load the Darwin identity FFI.
Linux uses pidfds; Darwin ARM64 uses pinned purego bindings to system task audit
tokens, without requiring cgo. Restricted process visibility, denied task/pidfd
APIs or syscall filters fail application activation/cleanup explicitly; there is
no numeric-PID signal fallback. See [installation](operations.md) and
[daemon operations](daemon.md).

## Linux prerequisites and installation

For adapting application commands across supported platforms, start with
[project adoption](adoption.md); use this guide for host/topology prerequisites.

Install Go at the version in `go.mod` (1.27.1, or a compatible newer patch), Git,
Make, `lsof`, and a C compiler/libc headers for the contributor race gate. Install
stock Caddy 2.11.6 separately for managed HTTPS and explicitly configure its
absolute path. Distro packages can lag these versions: Debian 13's original
Go 1.24 and Caddy 2.6.2 packages do not match this validation baseline. Toolchain
installation and system services remain operator responsibilities. Do not start a
separate Caddy service on Backlot's configured ports.

For container scenes, configure the local rootful Unix socket explicitly, normally
`unix:///var/run/docker.sock`. Docker group membership grants broad host access;
follow your machine's administrative policy. Backlot does not configure Docker or
pull images. Pre-pull/build declared images using Docker tooling. The representative
managed consumer is the [Go/Alpine application](../examples/container-https/README.md).

Build/install using [operations](operations.md), then start with a short private
state directory and run `doctor`. Directory mode must be 0700 and private files and
IPC 0600; existing unsafe permissions are rejected. Do not place private state in
a shared checkout. No root daemon, system trust changes or elevated ports are needed.

## Linux HTTPS and bridge consumers

Host/native/browser consumers use the ordinary canonical
`bl-<instance-label>.backlot.localhost` hostname and verified TLS. Ensure the host's
normal resolver returns loopback for `*.localhost`, independently of the browser.
Some Linux configurations resolve this through
[systemd-resolved synthetic records](https://www.freedesktop.org/software/systemd/man/257/systemd-resolved.service.html#Synthetic%20Records); files/DNS-only
setups and VPN DNS may not. Verify the actual native application as well as
`getent hosts bl-check.backlot.localhost`. Operator resolver setup is a prerequisite;
Backlot does not edit hosts files, DNS or VPN settings and does not replace the
canonical hostname. An unresolved hostname is a failed gate, never a trust bypass.

Rootful Linux bridge containers cannot normally reach a host loopback listener.
Explicitly configure an IPv4 address reachable through Engine `host-gateway`:

```yaml
version: backlot/v1
docker:
  endpoint: unix:///var/run/docker.sock
caddy:
  mode: managed
  executable: /usr/local/bin/caddy
  endpoint: http://127.0.0.1:2020
  https_port: 8443
  https_bind_address: 172.17.0.1 # Example only: verify your Engine's host gateway.
```

Omit `https_bind_address` for host-only use: HTTPS remains on IPv4/IPv6 loopback.
A specific canonical numeric IPv4 address adds a listener while preserving both
loopbacks. Explicit `0.0.0.0` instead binds all IPv4 interfaces plus IPv6 loopback;
this exposes HTTPS to other networks allowed by the host firewall. Select exposure
and firewall policy deliberately. No automatic interface detection, widening,
fallback or port takeover occurs. Admin always remains IPv4 loopback. A configured
address must exist at startup; changes require a deliberate maintenance decision
because retained gateway settings must match saved state. External mode does not
accept this setting; its operator owns listeners.

Keep project `host_mappings: [{origin: origin, host: host-gateway}]`, the read-only
`origin_ca_cert` file mount and explicit application trust assignment from the
[HTTPS guide](https.md#public-root-file-mounts-for-containers). The Engine expands
the token on each fresh create; neither a container's loopback nor an inferred
host address substitutes for it. Private signing keys/CA directories stay private.
Representative Go clients use ordinary DNS and `SSL_CERT_FILE`; clients that
force `.localhost` to container loopback (including curl versions with this
behavior) need separate compatibility work and are outside this tested consumer
contract. Do not replace TLS verification or use a different URL to hide failures.

Browser trust is separately configured by the operator. Linux CI uses a disposable
browser HOME/profile/NSS database containing only the public fixture root and
checks rejection before import and trusted canonical HTTPS/SSR after import.
This bounded browser evidence does not install system trust or promise every browser.
See [Chromium NSS certificate management](https://chromium.googlesource.com/chromium/src/+/main/docs/linux/cert_management.md)
for browser-specific stores; the fixture creates a private legacy `.pki/nssdb`,
which current Chromium still honors when it exists.

## WSL configuration guidance only

Run the Linux binary inside WSL2, preferably with the checkout, executable and
private state on the distribution's Linux filesystem (`~/...`), rather than
`/mnt/c`: Unix permissions, sockets and filesystem identity are part of the contract.
This WSL guidance does not cover native Windows binaries, a Windows daemon or
Windows process supervision; that planned work has separate #37/#38 gates.
WSL has not been QA'd by this feature.

Choose and configure the Docker endpoint explicitly. A Docker Desktop integration
socket inside WSL and a rootful Engine inside the distribution are different
network topologies. Check Engine `host-gateway`, actual HTTPS reachability, bind
interfaces and public-file mount visibility from the selected container; do not
assume Windows, WSL and container loopback refer to the same host. The Linux
bridge setting above is guidance, not proof for Docker Desktop/WSL.

WSL localhost forwarding/NAT or mirrored networking can affect whether a Windows
browser reaches Linux HTTPS; verify the configured WSL/Windows/firewall combination
separately. Keep Caddy administration on Linux loopback and never forward admin to
the LAN. A Windows browser resolves/trusts independently of Linux: import only the
public root into its appropriate Windows/browser trust store as an explicit
operator action, retaining CA signing keys inside private Linux state. Linux trust
does not establish Windows trust. Verify canonical HTTPS without warnings; Linux
CI, bridge tests and forwarding documentation establish no WSL browser guarantee.
