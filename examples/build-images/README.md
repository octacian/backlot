# Project-built container images

This optional workflow builds the current checkout using a native project command,
then runs a container from its accepted immutable local image ID. Prebuilt images
can continue using ordinary literal/input image declarations; they need no producer.

Install Docker tooling and provide `alpine:3.21` locally before running this example.
Configure machine `docker.endpoint` for that same local Engine. The project command
uses the Docker CLI's selected endpoint; explicitly set `DOCKER_HOST` in the build
component when it differs from that selection. Backlot does not inject credentials,
change Docker contexts, pull base images or implement a Dockerfile builder.
The example command's own builder behavior, dependencies and caching belong to
the project. A no-op cached build must still tag the supplied `APP_IMAGE` name.

From this directory, with a built Backlot binary and explicit private daemon:

```sh
backlot plan test --config /absolute/machine.yaml --json
backlot run test --config /absolute/machine.yaml --json
backlot scene start preview --config /absolute/machine.yaml --json
backlot restart INSTANCE_ID --config /absolute/machine.yaml --json
```

The offline plan keeps tag and ID references symbolic; it does not run Docker or
require the image. Every actual attempt gets a distinct tag. Only successful,
joined producers with a valid local tag handoff authorize consumers. Backlot pins
the Engine ID before creation, so later tag mutation cannot redirect the launch.
Use separate worktrees to vary `message` concurrently; declare checkout outputs
with concurrency groups if your project command also writes mutable build files.

Ordinary stop/start runs the each-start producer but uses its prior accepted ID,
even if the unused candidate differs. Explicit restart may accept a new image;
the same ID remains valid. Images with retained mounts conservatively reject a
changed produced ID after building, leaving the scene stopped and old pins/data
retained. To intentionally adopt it with an application/data compatibility warning:

```sh
backlot restart INSTANCE_ID --allow-image-change --json
```

That flag applies only to this restart's produced-ID comparison. It cannot bypass
other drift/ownership checks or change literal/input-image behavior. Reset instead
replaces retained data. Missing pinned images fail; there is no automatic rebuild
or fallback. Recovery preserves IDs and requires explicit restart.

Build images/tags are project-owned. Stop/reset/destroy do not delete them;
projects/operators manage disk use and preserve images needed by retained scenes.
This example intentionally has no image-GC, skip-build switch or result-file helper.
