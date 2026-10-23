# Normal Air and Vite development

This small example uses Go 1.27.1, Air 1.67.4, Node 26.8.1/npm and Vite 8.3.4.
Air rebuilds the Go HTTP API and starts its child in another process group; Vite
runs through ordinary `npm run dev`. Both remain in their live guardian's session.
Backlot supplies allocated loopback ports and the canonical gateway origin.

Install the tools separately, put `air`, `go`, `node` and `npm` on the daemon's
PATH, then prepare the pinned frontend dependencies:

```sh
go install github.com/air-verse/air@v1.67.4
cd examples/native-watchers
npm ci --ignore-scripts
```

Configure stock Caddy 2.11.6 and a managed gateway using the [HTTPS guide](../../docs/https.md).
System/browser trust and normal canonical hostname resolution are separate operator
prerequisites; do not bypass certificate warnings. Start a fresh v6 private state
directory with the prepared PATH, then the gateway and scene:

```sh
backlot daemon start --state-dir /absolute/private/watchers
backlot gateway start --state-dir /absolute/private/watchers --config /absolute/machine.yaml
backlot run dev --project "$PWD" --state-dir /absolute/private/watchers
```

Open the returned HTTPS URL. Edit `api-v1` in `main.go`: Air rebuilds and the API
response at `/api/` changes. Reload to fetch the API response again. Edit `web-v1`
in `main.js`: the browser updates through Vite's canonical WSS HMR connection,
without changing origins or disabling TLS. The Vite configuration derives the
allowed host and HMR endpoint from `APP_ORIGIN`. No wrapper/replacement watcher,
Backlot file watcher or automatic Backlot restart is involved. The declared `tmp`
output serializes conflicting builds in the same checkout; separate worktrees have
separate files/ports/origins and need their own `npm ci`.

A TCP readiness check proves current availability, not that the most recent source
compiled. Air may keep the previous application serving after a failed build.
Check `backlot logs --component api INSTANCE_ID`, correct the source and observe a
new response. Air/Vite versions beyond these pins and other watcher configurations
need their own evidence; this is not a broad watcher compatibility matrix.

Stop/destroy by the returned instance ID, then stop the gateway and daemon:

```sh
backlot stop --state-dir /absolute/private/watchers INSTANCE_ID
backlot destroy --state-dir /absolute/private/watchers INSTANCE_ID
backlot gateway stop --state-dir /absolute/private/watchers
backlot daemon stop --state-dir /absolute/private/watchers
```

Application session cleanup reports `observed_empty`: bounded identity-safe scans
found no live members twice, but unseen same-session forks or unsupported escapes
can remain and race restart or data deletion. Successful observation releases the
ordinary fences under that accepted risk. Known live members, denied checks,
exhausted budgets, lost authority or missing joins remain failures with fences.
`setsid`, privilege changes and unrelated supervisors are unsupported. Managed
Caddy keeps its stronger pinned-group proof. See the [native contract](../../docs/daemon.md#native-execution)
and [state upgrade instructions](../../docs/containers.md).

The opt-in `TestNativeWatchers` runs a prepared private copy of this project,
uses a private daemon/gateway, and checks real browser HMR with isolated Linux
HOME/profile/NSS public-root trust. Set the explicit watcher/Caddy/browser/certutil
test paths described in [testing](../../docs/testing.md); a skipped test is not
browser acceptance. No system/browser trust or DNS is changed by that fixture.
