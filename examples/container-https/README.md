# Native and container HTTPS example

Run a native Go frontend/API, Linux Go/Alpine SSR service and disposable test
through one canonical managed HTTPS origin. Target: macOS ARM64, Docker Desktop
Linux engine and stock native Caddy 2.11.6. Container clients use ordinary DNS and
Go root loading; no custom dialer or TLS verification bypass exists.

## Prepare

From the repository root, build application binaries explicitly:

```sh
go build -o examples/container-https/app ./examples/container-https
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o examples/container-https/app-linux ./examples/container-https
cd examples/container-https
docker --context desktop-linux pull alpine:3.21
docker --context desktop-linux build --pull=false -t backlot-container-https:local .
```

Image building/pulling belongs to the project. Backlot never performs it. Use an
image tag owned by this example; change both manifest image literals if you choose
another tag. Docker's image user must be able to read the mounted certificate;
this Alpine example uses its default root user. Backlot does not relax private
host CA permissions for other image users.

Copy `machine.example.yaml` to a private location outside the checkout. Replace
`YOUR_USER` with your actual home path, using the socket from
`docker context inspect desktop-linux --format '{{.Endpoints.docker.Host}}'`.
Confirm Caddy's absolute path and choose distinct available unprivileged admin and
HTTPS ports. The file uses loopback ports 2020 and 8443 as examples. Start your
owned daemon/gateway and separately establish host/daemon trust following the
[macOS gateway setup](../macos-quickstart/README.md#prepare-once), using this private
machine file. Browser trust is separate; importing a container root does not
establish host/browser trust. No global trust or DNS changes are made by Backlot.

## Run

From this example directory, using your private machine file:

```sh
backlot plan dev --config /absolute/private/machine.yaml --json
backlot run dev --state-dir /absolute/private/daemon --config /absolute/private/machine.yaml --json
backlot run test --state-dir /absolute/private/daemon --config /absolute/private/machine.yaml --json
```

Each scene gets its own canonical origin. `/api/value` routes to the native API;
`/ssr` routes to the container, which makes a verified HTTPS request to that same
origin's API. The native frontend handles `/`, `/ws` and `/redirect`. The terminal
container verifies SSR/API, three random WebSocket payloads, canonical redirects
and Secure/HttpOnly cookies. Its result and logs remain available after cleanup;
`backlot logs --component client --state-dir /absolute/private/daemon --json INSTANCE_ID`
shows the verification marker. The disposable scene cannot change the development
scene's origin or resources.

`host_mappings` explicitly select Docker's `host-gateway` token for the publication's
canonical hostname. Engine chooses the address on each new container create;
restart creates fresh containers, so no numeric IP or resolver helper is maintained.
Existing containers need recreation after Engine networking changes. Only this
bounded Docker Desktop topology is supported; no listener widening or fallback
occurs. `host.docker.internal` is not accepted as a substitute in this field.

Each consumer explicitly mounts `origin_ca_cert` read-only at `/app-trust/root.pem`.
The explicit `SSL_CERT_FILE` reference resolves to that path, while native
`APP_CA_CERT` resolves to the host public-root file. `APP_CA_CERT` is this Go
application's own native-client setting; Backlot does not detect runtimes or
inject it. Container Go uses its ordinary system pool plus `SSL_CERT_FILE`.
Only the existing public PEM file is mounted; no keys or CA directory are exposed.

To exercise a trust failure, temporarily add the following override to the
`client` component and run `test` again. The container exits nonzero with an
unknown-authority diagnostic; restore the declaration afterward.

```yaml
environment:
  assign:
    SSL_CERT_FILE: {literal: /missing.pem}
```

Node consumers use the same explicit mount and mapping, assigning
`NODE_EXTRA_CA_CERTS: {ref: {kind: resource, name: origin, field: ca_cert_path}}`
instead of `SSL_CERT_FILE`. Node reads extra roots at startup; application clients
that explicitly set `ca` must include the root themselves. See
[application trust](../../docs/https.md#explicit-application-trust).

Stop/restart/reset/destroy use normal Backlot lifecycle operations. Record the
persistent `instance.id`; stop removes its routes and consumers, restart retains
its origin, and destroy releases its owned resources. Gateway stop/restart refuses
active published scenes. Destroy example instances before stopping your owned
managed gateway. Remove only your explicitly owned image/binaries when finished;
never prune shared Docker state.
