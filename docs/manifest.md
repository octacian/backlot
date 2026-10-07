# Offline planning and manifest v1

Milestone 1 implements read-only planning. It does not run a daemon, execute
application commands, reserve ports, generate secrets, inspect provider health,
create containers, or register routes. Later milestones consume this contract.

```sh
go run ./cmd/backlot plan test --project examples/adoption --json -- -run TestExample
go run ./cmd/backlot plan backend-test --project examples/fullstack \
  --config examples/fullstack/machine.example.yaml --json -- -run TestOrders
```

The full command is `backlot plan <scene> [--project PATH] [--config PATH]
[--json] -- <terminal-job args>`. Flags follow the scene. Each flag occurs once;
path flags take separate values. Everything after the first `--` goes unchanged
to the disposable scene's terminal job only, after its declared arguments.
Arguments without a terminal job are errors. Scene names are project-defined.
Human output summarizes selected work. JSON emits exactly one shared
`api/v1.PlanResponse` or `api/v1.ErrorResponse` on stdout; failures return nonzero
and diagnostics go to stderr. Errors include a code, field context and recovery
instruction. Argument-parser failures before entering the plan action follow the
CLI library's normal diagnostic behavior.

## Discovery and configuration

Without `--project`, search the working directory and parents, including the
nearest directory containing a `.git` file or directory, then stop. Without a
Git checkout, search to the filesystem root. Exactly one of `backlot.yaml`,
`backlot.yml`, or `backlot.json` must exist in the project directory. Multiple
candidates are an error, even with an explicit file override. `--project` selects
an exact directory or one of those filenames. Paths are canonicalized through
symlinks; the checkout path is the nearest Git root, or the project directory.
No Git subprocess runs. This is path provenance, not a durable runtime identity;
moved-checkout reconciliation belongs to milestone 2.

`--config` reads an explicit YAML/JSON machine file relative to the working
directory. Otherwise use `backlot/config.yaml` beneath Go's `os.UserConfigDir()`
(on macOS, `~/Library/Application Support`). If absent, machine configuration is
optional. Container scenes require Docker settings; published scenes require
Caddy settings. Native-only unpublished scenes need neither. Only the selected
scene's tools and referenced inputs are required. Supplied configuration is
always strictly validated, including unused settings. Tool/input overrides must
name project declarations. File errors are actionable and never print contents.

All documents use `version: backlot/v1` with exact, case-sensitive field names.
The shared Go types in [api/v1](../api/v1/manifest.go) define the field inventory;
JSON and YAML use the same snake_case names. Input is bounded to 1 MiB per file
and 64 nested levels. Unknown or duplicate fields/declarations, trailing documents,
nulls, incorrect scalar types, custom YAML tags, anchors, aliases, merge keys and
non-string mapping keys are rejected. Quote strings that look like booleans or
numbers. JSON must also obey JSON syntax; YAML conveniences are unavailable in
`.json` files. Identifiers start with a lowercase letter and contain at most 63
lowercase letters, digits, underscores or hyphens.

## Project declarations

A manifest declares `project`, `tools`, `inputs`, `environments`, `resources`,
`outputs`, `components` and `scenes`. `components` and `scenes` must be nonempty.
Project is a stable identifier, independent of branch names. Reusable maps are
selected explicitly; there is no inheritance, implicit dependency inclusion,
shell expansion or general templating.

`tools` maps names to executable names or paths. A native `command` contains a
`tool` name and an `args` array. Machine `tools` overrides selected executable
paths. Relative executable paths resolve from the project directory; bare names
use the planner's PATH. Planning checks regular-file executable availability with
Go's `exec.LookPath`, without executing the tool. An explicit command such as
`sh` with `args: [-c, ...]` is the application's deliberate shell invocation.
Do not place credentials in command arguments, tool paths or identifiers; use
sensitive environment mappings instead.

`inputs` maps names to `{secret: true}` or `{}`. Referenced inputs must have
nonempty values in machine `inputs`. Secret inputs remain redacted in results.
An unreferenced input does not block another scene. Images can be nonempty
literal values or declared input references; there is no image-building engine.
Build jobs may invoke the project's existing tools.

`resources` maps names to a `kind`: `port`, `directory`, `volume`, `network`,
`secret`, or `origin`. Every resource is instance-owned intent, never an existing
external resource to adopt by name. A scene selects its resources, and components
explicitly attach the resources they consume. Secret generations will be retained
with persistent storage; the offline planner does not generate or rotate them.

`outputs` maps names to `{path: web/dist, concurrency_group: web-build}`. Paths
are safe project-relative paths, without traversal, backslashes or drive names.
Overlapping paths must share a group. Selected existing output ancestors cannot
escape the project through symlinks. Components list their outputs. A future
executor must serialize these groups per canonical checkout across scenes and
runs; a plan is not a lock. Backlot cannot infer undeclared command side effects.

## Components and scenes

Components declare `kind: service|job` and `runtime: native|container`. Native
work requires `command`; container work requires `image` and optionally direct
`args`. Native work cannot declare container `image`, `args` or `mounts`.
Containers use declared `resources` and `mounts`: each mount names exactly one
attached `resource` (volume/directory) or `output`, an absolute unique `target`,
and optional `read_only`. No external volume/container adoption is implied.

Services declare named `ports`, each with an attached port `resource`, and an
explicit `readiness` probe. Container ports additionally require `container_port`
in 1..65535 for the image's listener; native ports omit it. Jobs cannot declare readiness or ports. A port
resource cannot be claimed twice in one scene. Readiness probes use `kind: tcp`,
`http`, or `command`, and a positive `timeout` up to 24h. Command probes contain
an explicit native tool command, whose availability is checked. Network probes
contain a `target` value: TCP uses a symbolic port or literal `host:port`; HTTP
uses a symbolic origin or literal HTTP(S) URL without credentials. A symbolic
service port carries enough identity for a future adapter to resolve its address;
no numeric port is invented here.

Jobs require `policy: fresh-only|each-start`. Fresh-only jobs declare nonempty
`initializes`, naming attached volume/directory generations. Two selected jobs
cannot initialize the same generation. Each-start jobs cannot declare initializes.
The executor will record fresh initialization only after successful completion;
planning preserves eligibility policy, without claiming a job was run or skipped.

`depends_on` is an array of `{component: name, condition: ready|completed}`.
Services require `ready`, jobs require `completed`. Dependencies must be selected
in the scene. Duplicate/self dependencies and cycles anywhere in the reusable
manifest are errors. Values referring to services do not imply readiness gates;
projects must declare them. Plan components appear in dependency-first order,
using the explicit scene and dependency list order for independent work.

Scenes require `lifetime: persistent|disposable` and a nonempty `components`
selection. Disposable scenes require a selected each-start `terminal_job`;
every selected component must transitively gate that job. Persistent scenes
cannot designate a terminal job. All reusable declarations and scene graphs are
validated before reading seeds or checking tool availability.

`publish` selects an origin `resource`, nonempty `routes`, and an aggregate HTTP
`probe` referring to that origin. Each route contains a unique absolute `path`,
a selected `service`, its named `port`, and `prefix: preserve|strip`. This bounded
structure does not accept Caddyfiles. The aggregate probe is separate from
component readiness, to be observed after route registration by the executor.
A component readiness probe cannot depend on the published origin, which would
create a startup cycle.
Origin references require scene publication; Caddy setup is only checked for
presence/validity, with no network call or certificate/DNS check.

## Values and environment

A value contains exactly one `literal` string or `ref` object, optionally
`secret: true`. A reference has explicit `kind`, `name`, `field` and, only for a
service port, `port`. Supported shapes are:

| Kind | Name | Field | Planning behavior |
| --- | --- | --- | --- |
| `input` | Declared input | Omit | Resolve supplied machine value |
| `instance` | Omit | `project`, `checkout`, `scene` | Resolve known provenance |
| `instance` | Omit | `id` | Symbolic; no instance allocated |
| `resource` | Attached/selected resource | `port`, `path`, `name`, `value`, `url` according to kind | Symbolic; secret values redacted |
| `service` | Selected service | `host` or `port` (with named `port`) | Symbolic |
| `output` | Attached output | `path` | Resolve project-relative output path |

Resource fields are `port.port`, `directory.path`, `volume.name`, `network.name`,
`secret.value`, and `origin.url`. Unused/extra reference fields are rejected.
There are no concatenation, expressions, variable substitutions or embedded
`${...}` references. Literal strings retain their literal characters.

A component explicitly lists `environment_sets`, then declares its own
`environment`. Each set/environment may have `pass_through`, `seeds`, `assign`
and `required`. Apply these phases across all included sets, in their listed
order followed by the component environment:

1. Launch baseline: existing PATH, HOME, TMPDIR, TMP, TEMP, SystemRoot only.
2. Selected inherited keys from all pass-through lists.
3. Seed files from all seed lists, in declaration order.
4. Explicit assignments from all assignment maps; the component wins last.

Check all required environment keys after layering; missing or empty values
fail. Symbolic references satisfy presence, without claiming allocation.
Assignment sensitivity follows `secret: true`, secret input declarations or
secret resources. For conservative safety, **all inherited and seed values are
redacted** in ordinary plans, including the baseline. Non-sensitive explicit
literals remain visible. Redacted values carry neither a literal nor a symbolic
payload; the plan never serializes raw environment definitions or machine config.

Seeds are UTF-8 regular files bounded to 1 MiB, with safe project-relative paths
and no symlink escape. Blank lines and lines whose first non-whitespace character
is `#` are ignored. Other lines must be literal `KEY=value`; keys use standard
environment identifier syntax. CRLF is accepted. Spaces, quotes, `#`, `$`,
backticks and `$(...)` in values are retained literally. No sourcing, export,
multiline quoting, interpolation or execution occurs. Duplicate keys inside a
file fail; later files may override earlier files. Backlot never creates or
rewrites application configuration files.

## Machine schema and results

Machine configuration uses the same version and may declare:

- `tools`: declared tool executable overrides.
- `inputs`: declared input values, including machine-held credentials.
- `docker`: `endpoint` as a local `unix:///...` socket URL.
- `caddy`: HTTP(S) admin `endpoint` without credentials/query/fragment, owned
  identifier `scope`, concrete `domain_suffix`, and gateway-reachable `host_address`.
- `storage`: `directory`, positive duration `retention_age` and positive integer
  `retention_bytes`. These settings are validated intent for a later daemon.

The plan records canonical checkout and manifest paths, SHA-256 digests of source
manifest/config bytes, lifetime, terminal job, selected components/resources,
outputs/groups and publication intent. Digests capture drift/provenance, not
provider health or completed allocations. They should be treated as local
provenance metadata, not as a way to publish sensitive input files. Shared
[planning request/result/error types](../api/v1/plan.go) are the actual offline
operation contract; this milestone adds no speculative HTTP endpoints.

The [adoption fixture](../examples/adoption/backlot.yaml) shows a small native
project. The [fullstack fixture](../examples/fullstack/backlot.yaml) shows shared
MariaDB/native/frontend definitions across development, production preview,
backend tests and UI tests. They exercise planning only: application commands
are illustrative adoption inputs, and providers are not provisioned. Their
machine example contains illustrative endpoints and an image name, no credentials.
