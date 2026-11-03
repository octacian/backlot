# Package-local native commands

This small Go app runs its existing dev/build/test commands in `app` through
`command.working_directory`, without a directory-changing wrapper. The server and
test read `message.txt` relative to their package. Go and `lsof` must be available
to the daemon; no containers or providers are needed.

From this directory, with Backlot installed and a compatible daemon running:

```sh
backlot plan test --json
backlot run test --json
backlot run build --json
backlot scene start dev --json
backlot scene stop dev --json
```

Use `--config /absolute/private/machine.yaml` to select an explicit machine file
and `--state-dir /absolute/private/state` for an owned daemon when testing in
isolation. A provider-free machine file contains only `version: backlot/v1`.
Start that daemon with `backlot daemon start --state-dir /absolute/private/state`.
The dev result reports the allocated HTTP port; requesting it returns
`package ready`. Stop the scene before shutting down an owned daemon.

Build argv `../build/app` is relative to the selected `app` cwd. Output `build`
and artifact `build/app` stay relative to the manifest. See the
[working-directory contract](../../docs/manifest.md#native-working-directories)
for validation, readiness inheritance and retained drift behavior.
