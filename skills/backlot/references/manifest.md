<!-- Generated from docs/manifest.md; run make skills-generate. -->

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

`inputs` maps names to `{secret: true}` or `{}`, optionally with descriptions
and secret generation hints. Referenced inputs must have nonempty effective
values in registered private project `inputs`. Secret inputs remain redacted in results.
An unreferenced input does not block another scene. Images can be nonempty
literal values, declared input references or explicit build-image ID references;
there is no image-building engine. See [build images](manifest.md#execution-scoped-build-images).
Build jobs may invoke the project's existing tools.

`resources` maps names to a `kind`: `port`, `directory`, `volume`, `network`,
`secret`, `origin`, or `allocation`. An allocation names a declared
`shared_service`; see [shared-service declarations](manifest.md#shared-service-declarations).
Every resource is instance-owned intent, never an existing
external resource to adopt by name. A scene selects its resources, and components
explicitly attach the resources they consume. Secret generations are retained
with persistent storage; the offline planner does not generate or rotate them.

`outputs` maps names to `{path: web/dist, concurrency_group: web-build}`. Paths
are safe project-relative paths, without traversal, backslashes or drive names.
Overlapping paths must share a group, including canonical aliases through
symlinks and missing suffixes, across scene declarations. Selected existing output
ancestors cannot escape the project through symlinks. Components list their
outputs. The executor serializes these groups per canonical checkout
across scenes and runs; a plan is not a lock. Backlot cannot infer undeclared
command side effects.

## Declared native tasks

Enable native `job` components with `policy: each-start` in a persistent scene's
`tasks` list for explicit invocation. `components` selects automatic startup work;
a component in both lists also runs at startup. Tasks cannot initialize resources
or produce build images. Every attached resource and dependency must already be selected by
the parent; dependencies test accepted ready services or successful/skipped parent
jobs without launching work.

```yaml
components:
  maintenance:
    kind: job
    runtime: native
    policy: each-start
    command: {tool: app, args: [maintenance]}
    resources: [database]
    environment:
      assign:
        PASSWORD: {ref: {kind: resource, name: database, field: password}}
scenes:
  dev:
    lifetime: persistent
    components: [server]
    resources: [database]
    tasks: [maintenance]
```

This fragment assumes the application's existing `app`, `server` and allocation
resource declarations. Task environments use native endpoints and the parent's
accepted paired credentials, including composed values; `instance.id` means the
parent ID. Sensitive values stay in environment mappings, never argv. Task output
and artifact declarations use the existing project-relative anchors.

Each new parent attempt validates and privately captures every enabled task's tools,
cwd, seeds and inputs, even for unused tasks. Task outputs are validated without
acquiring startup locks. Invocation rejects relevant changes to current configuration
or referenced parent contracts and unavailable inputs; explicit parent restart is
required. Unrelated task edits do not invalidate the capture. Legacy snapshots
have no task captures, and ordinary joins never add them. See
[invocation](cli.md#declared-task-invocation) and
[parent ownership](daemon.md#tasks-borrowing-a-ready-parent).

## Native working directories

Native `command.working_directory` selects an existing directory relative to the
manifest directory. Omission keeps the manifest directory; `.` explicitly selects
it. Paths cannot be absolute, contain traversal, backslashes or drive names, or
escape the manifest directory through symlinks. Planning checks filesystem
metadata without executing tools; launch rechecks the retained canonical target
and rejects a changed, missing or invalid directory.

A native service's command readiness probe inherits the service cwd when its own
`working_directory` is omitted. An explicit probe override is always relative to
the manifest, including `.` to return to its directory. Command probes on container
services also execute on the host and support overrides; their omitted default
remains the manifest directory. Container image `WORKDIR` is unchanged.

Relative argv paths are interpreted by the application from the selected cwd.
Relative tool paths, discovery, output declarations, artifact paths and seed files
keep their existing manifest/project anchors. For example, a build in `app` may
use argv `../build/app`, with output `build` and artifact `build/app`.

Plans and retained snapshots carry the effective canonical directory; omitted
fields in older snapshots retain the manifest-directory default. Healthy work
keeps its accepted cwd and reports unapplied drift, including symlink retargets
with unchanged manifest bytes. Compatible start/restart may accept an ordinary
command or probe cwd change; changing a retained fresh-only initializer's effective
cwd requires reset/destroy. This is cooperative native execution, not a sandbox
against concurrent host filesystem mutation or application file access.

## Components and scenes

Components declare `kind: service|job` and `runtime: native|container`. Native
work requires `command`; container work requires `image` and optionally direct
`args`. Native work cannot declare container `image`, `args` or `mounts`.
Containers use declared `resources` and `mounts`: each mount names exactly one
attached `resource` (volume/directory), `output`, or `origin_ca_cert` (the
published managed origin public root), an absolute unique `target`, and optional
`read_only`. Origin CA mounts require `read_only: true` and a file target that
does not overlap any other mount. The unused source may be omitted or an empty string;
exactly one source must have a nonempty value. No external volume/container
adoption is implied.

Container `host_mappings` explicitly list `{origin: origin, host: host-gateway}`.
The managed origin must be attached, selected and published. Only its canonical
hostname is mapped for that consumer. The sole supported `host` is the explicit
Engine `host-gateway` token; DNS names, numeric addresses, arbitrary aliases,
duplicates and native declarations are rejected. Docker expands the token when
creating each container, including fresh containers on restart. No helper image,
host-DNS lookup, copied IP or global resolver change is involved. Planning
preserves this intent without contacting Docker. Unsupported Engine mappings
fail container creation; there is no implicit fallback.
See [container HTTPS](https.md#public-root-file-mounts-for-containers).

Services declare named `ports`, each with an attached port `resource`, and an
explicit `readiness` probe. Container ports additionally require `container_port`
in 1..65535 for the image's listener; native ports omit it. Jobs cannot declare readiness or ports. A port
resource cannot be claimed twice in one scene. Readiness probes use `kind: tcp`,
`http`, or `command`, and a nonnegative `timeout` up to 24h (`0s` is unlimited, subject to
other finite execution budgets). Command probes contain
an explicit native tool command, whose availability is checked. Network probes
contain a `target` value: TCP uses a symbolic port or literal `host:port` with a
numeric port in 1..65535 (TCP service-name ports are unsupported). HTTP uses a
symbolic origin or literal HTTP(S) URL without credentials; composed TCP/HTTP
targets follow [explicit value composition](manifest.md#compose-explicit-values). Explicit
HTTP(S) ports, including Caddy admin endpoint ports, must be in 1..65535; omitted
ports retain the scheme default and an explicitly empty port is invalid. A symbolic
service port is resolved at execution; offline planning invents no numeric port.

Jobs require `policy: fresh-only|each-start`. Fresh-only jobs declare nonempty
`initializes`, naming attached volume/directory/allocation generations. Two selected jobs
cannot initialize the same generation. Each-start jobs cannot declare initializes.
The executor records fresh initialization only after successful completion;
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

Persistent scenes may declare ordered startup fixture selections:

```yaml
startup_details:
  - {component: seed, fixture: email, label: Login email}
  - {component: seed, fixture: password, label: Login password}
```

Each selection names an existing fixture on a selected component and a nonempty
single-line label. Nonempty startup details are invalid on disposable scenes.
Values still come from ordinary component fixtures and their existing sensitivity
propagation; this introduces no expressions or duplicate value declarations.

The selection authorizes automatic display of its sensitive values after successful
`run`, `scene start`, `restart` and `reset`, including already-running starts.
Human output prints labels/values after instance/URL information; JSON emits one
result with a transient `startup_details` object containing `attempt`, ordered
`details`, or a sanitized `warning`. No successful summary appears on failed
readiness. If summary retrieval fails after readiness, startup still succeeds
with its identity/URL and an unavailable warning; incomplete details are omitted.

Starting or already-ready scenes keep recorded selections/values and report
configuration changes. New attempts use compatible current inputs and
startup-details selections. Restart/reset adopt current selections through existing
compatibility and snapshot rules. A startup replaced before observed readiness
conflicts instead of reporting a replacement attempt's summary. Ordinary plans,
status/follow, inspect, diagnostics, fixture discovery, execution results and
exports remain redacted. Keep credentials out of labels/identifiers and protect
startup output as sensitive. See the [seeded example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/macos-quickstart/README.md#seeded-demo-account)
and [decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md).

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

A value contains exactly one `literal`, `ref`, `format`, `host_port`, `url_path` or `file_path` payload, optionally
`secret: true`. A reference has explicit `kind`, `name`, `field` and, only for a
service port, `port`. Supported shapes are:

| Kind | Name | Field | Planning behavior |
| --- | --- | --- | --- |
| `input` | Declared input | Omit | Resolve registered private project value |
| `instance` | Omit | `project`, `checkout`, `scene` | Resolve known provenance |
| `instance` | Omit | `id` | Symbolic; no instance allocated |
| `resource` | Attached/selected resource | `port`, `path`, `name`, `value`, `url` according to kind | Symbolic; secret values redacted |
| `service` | Selected service | `host` or `port` (with named `port`) | Symbolic |
| `output` | Attached output | `path` | Resolve project-relative output path |

Resource fields are `port.port`, `directory.path`, `volume.name`, `network.name`,
`secret.value`, `origin.url`, `origin.hostname` or `origin.ca_cert_path`, and allocation
`host`, `port`, `database`, `username`, `password`. Allocation passwords are
automatically sensitive. `ca_cert_path` is only for
explicit component environment assignments (including explicitly selected reusable sets), requires
a selected/attached/published origin and explicit managed Caddy configuration,
and stays symbolic offline. Container assignments also require an explicit
`origin_ca_cert` mount and resolve to its target. See [explicit application trust](https.md#explicit-application-trust). Unused reference fields may be omitted or
empty strings; nonempty extra values are rejected. Prefer omission when authoring
manifests.
`origin.hostname` is the canonical hostname without scheme or port, with the same
attachment, selection and publication requirements as `origin.url`.

### Compose explicit values

Use compositions in explicit environment assignments, native/container argv
entries and TCP/HTTP readiness targets. Operands are recursive values, with the
same reference and context checks at every level. Images and fixtures keep their
existing literal/reference forms. Each composition has a 1 MiB **rendered byte**
limit, checked before rendering known offline output and final hydrated output,
including URL escaping and filesystem cleaning; raw operand length is not the
rendered size when later cleaning can shorten it. Existing literals, references and scalar argv keep their
previous compatibility and limits.

| Payload | Behavior |
| --- | --- |
| `format: {template: '%s:%s', values: [Value, Value]}` | Substitute the next string for each `%s`; `%%` emits one percent. Require exact operand count; reject every other verb, flag, width, precision or index. |
| `host_port: {host: Value, port: Value}` | Validate a hostname/numeric IP and decimal port in 1..65535; join IPv6 with brackets. Supply the host without brackets. |
| `url_path: {base: Value, parts: [Value, ...]}` | Append parts in order beneath a credential-free absolute HTTP(S) base without query/fragment. One separator at each boundary; a single leading slash does not replace the base. Empty parts are no-ops; `[]` preserves the validated base exactly. Preserve interior characters and the last nonempty part's trailing slash. |
| `file_path: {base: Value, parts: [Value, ...]}` | Join and clean base plus parts using Go `filepath.Join` for native consumers and POSIX `path.Join` for containers on every daemon OS. `[]` still normalizes the base. `..` may escape the base; empty paths and separators follow the selected library. Reject NUL in every operand, including parts later removed by cleaning. |

Both path constructors require `base` and a non-null `parts` array; the former
unshipped URL `path` field is not accepted. Filesystem joining only constructs a
string: no filesystem access, containment, mount rewriting or conversion of native
paths to container paths. Existing working-directory, mount and output policies
stay unchanged. Filesystem-looking URL/readiness values still require valid final
destinations. Every operand retains its reference, sensitivity and context checks,
even when cleaning removes its text.

URL paths reject traversal (including recursively encoded dot segments/separators),
backslashes, authority replacement, and query/fragment syntax. Go `net/url` owns
URL parsing and escaping; valid existing path escapes are interpreted as path
characters and escaped for the resulting URL. Formatting performs no numeric
conversion, recursive percent processing, automatic credential escaping or shell
expansion. Application connection-string conventions remain application-owned.
For example:

```yaml
environment:
  assign:
    DB_ENDPOINT:
      host_port:
        host: {ref: {kind: service, name: database, field: host}}
        port: {ref: {kind: service, name: database, field: port, port: sql}}
    API_URL:
      url_path:
        base: {ref: {kind: resource, name: public, field: url}}
        parts: [{literal: api}, {literal: /v1/}]
    REPORT_PATH:
      file_path:
        base: {literal: reports}
        parts: [{literal: results}, {literal: junit.xml}]
command:
  tool: app
  args:
    - serve
    - format:
        template: '--port=%s'
        values: [{ref: {kind: resource, name: http_port, field: port}}]
```

Each argv entry resolves to exactly one argument: spaces, percent signs and
special characters are retained without splitting or quoting. Forwarded CLI
arguments remain literal strings. Sensitive argv, including nested secret
references, is rejected before effects. Environment sensitivity includes the
outer value and every operand; redact the whole sensitive composition.

Offline plans partially resolve known operands while preserving typed unresolved
compositions and reference provenance. There are no environment references or
assignment-order dependencies. Check every assignment even when a later layer
overrides it. Runtime endpoints follow the actual consumer: host-native command
probes use host endpoints even on container services; workload containers use
private/reachable endpoints; daemon network probes use owned host destinations.
Filesystem constructors use that same actual consumer's path syntax. Shared
container/host-probe environment values retain typed filesystem constructions
until each consumer resolves them, including nested private operands.
Nested CA references remain limited to explicit environment assignments, with
existing publication and mount requirements. A reference in a URL path grants no
destination ownership, publication-cycle exemption or trust. Aggregate composed
probes retain the selected canonical origin and ordinary TLS/marker checks.

There is no concatenation operator, expression language, variable substitution or
embedded `${...}` reference. Literal strings retain their literal characters.

A component explicitly lists `environment_sets`, then declares its own
`environment`. Each set/environment may have `pass_through`, `seeds`, `assign`
and `required`. Apply these phases across all included sets, in their listed
order followed by the component environment:

1. Native launch baseline: existing PATH, HOME, TMPDIR, TMP, TEMP, SystemRoot
   only. Containers keep image defaults; host values enter only through explicit
   pass-throughs, seeds or assignments.
2. Selected inherited keys from all pass-through lists.
3. Seed files from all seed lists, in declaration order.
4. Explicit assignments from all assignment maps; the component wins last.

Check all required environment keys after layering; missing or empty values
fail. Symbolic references satisfy presence, without claiming allocation.
Assignment sensitivity follows `secret: true`, secret input declarations or
secret resources, including every nested operand. For conservative safety, **all inherited and seed values are
redacted** in ordinary plans, including the baseline. Non-sensitive explicit
literals remain visible. Redacted values carry neither a literal nor a symbolic
or composition payload; the plan never serializes raw environment definitions or machine config.

Seeds are UTF-8 regular files bounded to 1 MiB, with safe project-relative paths
and no symlink escape. Blank lines and lines whose first non-whitespace character
is `#` are ignored. Other lines must be literal `KEY=value`; keys use standard
environment identifier syntax. CRLF is accepted. Spaces, quotes, `#`, `$`,
backticks and `$(...)` in values are retained literally. No sourcing, export,
multiline quoting, interpolation or execution occurs. Duplicate keys inside a
file fail; later files may override earlier files. Backlot never creates or
rewrites application configuration files.

Components may also declare named `artifacts` and `fixtures`; see the
[disposable evidence contract](evidence.md#declared-artifacts-and-fixtures).

## Machine schema and results

Machine configuration uses the same version and may declare:

- `tools`: declared tool executable overrides.
- `docker`: `endpoint` as a local Unix socket URL (canonical `unix:///...`;
  `unix:/...` is also accepted), optional container-to-native `host_address` and
  numeric mapped-port `publish_address` (default `127.0.0.1`).
- `caddy`: explicit `mode: managed` selects stock native Caddy, its `executable`,
  loopback admin `endpoint` (default `http://127.0.0.1:2020`) and HTTPS port
  (default 8443). Optional `https_bind_address` is an explicit Linux/container
  reachability choice. Omitted mode selects external Caddy with operator-owned
  `endpoint`, `scope`, `domain_suffix`, `host_address` and `https_port` (default
  443). See [HTTPS setup and routing](https.md).
- `storage`: `directory`, positive duration `retention_age` and positive integer
  `retention_bytes`. These scene configuration settings remain validated intent. Configure the running
  daemon with `--state-dir`, `--retention-age` and `--retention-bytes`; scene input
  never changes the daemon-wide policy.

These are **effective** tool/provider settings for selected work. Machine
`storage` fields are validated but **reserved intent**, not active daemon policy.
`config show` reports local composition/prospective defaults; `daemon config`
reports actual captured state/socket/lease/retention policy. Changing a machine
file or repeating `daemon start` does not change a running daemon's policy.
Stop/start it deliberately to change startup flags or inherited environment.

Configuration sources are distinct: the manifest declares names/default tools;
the optional machine file provides shared tools/providers; the registered private
project layer supplies input values and tool overrides. Private tools win over
machine tools, which win over manifest defaults. Relative paths resolve from the
project; planning uses caller PATH while execution uses daemon PATH. Source byte
digests and effective configuration drift are different facts; see
[private provenance](manifest.md#registered-private-project-configuration) and
[current versus recorded diagnostics](daemon.md#scoped-provider-diagnostics).

The plan records canonical checkout and manifest paths, SHA-256 digests of source
manifest/config bytes, lifetime, terminal job, selected components/resources,
outputs/groups and publication intent. Digests capture drift/provenance, not
provider health or completed allocations. They should be treated as local
provenance metadata, not as a way to publish sensitive input files. Shared
[planning request/result/error types](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/api/v1/plan.go) are the actual offline
operation contract.

The [adoption fixture](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/adoption/backlot.yaml) shows a small native
project. The [fullstack fixture](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/fullstack/backlot.yaml) shows shared
MariaDB/native/frontend definitions across development, production preview,
backend tests and UI tests. They exercise planning only: application commands
are illustrative adoption inputs, and providers are not provisioned. Their
machine example contains illustrative endpoints; its private example supplies the image name.

Human `plan` output shows command/readiness executable paths as caller/offline
selections, with a working directory when it differs from the manifest directory;
JSON uses `executable` and optional effective `working_directory` fields. Missing-tool errors name
the attempted candidate and winning declaration file. Bare missing names remain
bare; relative paths resolve from the project. These are launcher selections,
not runtime version checks or proof of execution.

## Registered private project configuration

Run `backlot project setup --project /path/to/project` once in a terminal. Setup
walks declared inputs in name order, shows their descriptions, hides custom
secret entry, and requires explicit acceptance before generating a secret.
Declare `description: Local API credential` on an input to explain it;
`generate: random-hex-32` is permitted only with `secret: true` and generates 32
cryptographically random bytes encoded as 64 lowercase hex characters. The hint
never generates during ordinary planning or execution. Every declared input must
have an effective nonempty value to complete setup, including inputs used only
by other scenes. Setup validates tool syntax without executing tools or checking
unselected executable availability.

For automation, supply `--from /absolute/private.yaml` or `--from -` for stdin:

```yaml
version: backlot/v1
inputs:
  api_token: replace-through-private-input
tools: {} # omit tools when no override is needed
```

The document permits only `version`, `inputs`, and `tools`. Do not copy real
credentials into committed examples. File/stdin input is bounded and strictly
decoded as YAML/JSON; a file's extension selects its format. `--generate NAME`
explicitly accepts generation in automation. Values are never accepted as raw
command-line arguments. `--json` emits one named `ProjectSetupResponse` or
`ErrorResponse`; interactive prompts go to the terminal on stderr.

For a local tool override, run `backlot project setup --edit --project /path/to/project`
and set the declared tool's executable. Private overrides win over machine tools,
which win over manifest defaults, even when values match. Recheck the daemon's
[current selection](daemon.md#scoped-provider-diagnostics) after editing.

Completed setup is inspection: repeating the command reports the association,
exact private file path, configured/missing input names and tool paths without
writing, rotating, or prompting. New declarations appear missing. Use `--edit`
for a terminal walkthrough, `--from` for a structured patch, or explicit
`--generate`, `--remove-input NAME`, and `--remove-tool NAME` edits. Unspecified
private entries are preserved. Removal must name an existing private entry and
cannot accompany a replacement/generation for the same name. Input removal has no
fallback: if the input is still declared, removal fails setup completeness. Tool
removal can expose a machine override or manifest tool default. After a rename,
remove the retired name and supply its replacement in the same update.
Ordinary reads remain strict, while explicit edits defer only old private key
membership until the complete candidate is validated. Invalid, incomplete,
cancelled, conflicting or failed edits never partially publish.

Ordinary setup never prints input values. Only local
`project setup --dangerously-reveal-secrets` inspection includes configured
secret values; it rejects mutation options and never affects daemon outputs.
Keep its output private. Paths/descriptions/tool paths must not contain secrets.

Setup stores a registry at `backlot/projects.json` under `os.UserConfigDir()` and
immutable private generations under `backlot/projects/`, outside the checkout.
Managed directories/files must be user-owned and private (0700/0600); unsafe
existing boundaries are rejected rather than repaired. Import files are copied,
not ongoing dependencies. Git common-directory path plus filesystem identity
and repository-relative manifest path bind the association. Linked worktrees
reuse it; monorepo manifest paths isolate it. Names/remotes and independent
clones do not enroll. Moved/replaced repository identities require explicit
`project setup --reassociate` and explicit new values; old credentials are not
implicitly imported. This repairs private setup only; retained daemon projects require
[separate preview/apply](cli.md#reassociate-retained-project-data).
Non-Git projects bind canonical directory identity and manifest filename.

Ordinary plan/prepare/run/restart/reset and scene diagnostics automatically read
the registered layer. Validate manifest, machine, and private sources independently,
then compose tools by key with private overrides winning. Private project
configuration is the sole input-value source; machine `inputs` rejects even empty,
null or unused. Remove old fields and supply project values explicitly; no shim,
import or migration occurs. Invalid overridden/unused
entries still fail. Registered missing/unsafe/invalid files fail explicitly,
including for scenes that use no inputs. Unregistered projects can select work
that needs no inputs; selected missing values direct you to project setup. Ordinary operations create no registry, locks, or secrets.
Relative private tools resolve from the project directory; bare PATH names and
absolute paths remain supported. Only selected scene inputs/tools are required.
Provider settings stay in the shared machine document; there is no per-command
private-file override, recursive include, or deep merge.

`config_digest` retains the machine-file byte digest. The additive
`effective_config_digest` records composed validated inputs/tools/providers;
source-only formatting or overridden tool changes do not become effective drift.
Starting or already-ready scenes keep recorded configuration and report changes.
New attempts and explicit restart/reset use current layers subject to
retained-resource safety checks. Stop, destroy and recovery use recorded snapshots.
Diagnostics distinguish launch/current private sources and effective configuration;
export records the attempt's effective digest without private contents.

Setup serializes publication under an advisory registry lock and fails if captured
sources changed while answers were collected. Do not manually edit managed files
while setup runs. Registry replacement is the commit point: a later sync failure
reports `setup_committed_uncertain`; inspect before retrying. Old immutable
private generations are retained for readers and may contain old secrets. There
is no automatic collector. Operators must deliberately remove obsolete generations
only after readers finish; rotation does not erase daemon snapshots, backups or
memory. Keep private storage on a local filesystem supporting atomic rename/locks.

Environment sets and literal seed files remain useful when external project
association and executable overrides are unnecessary. They keep their existing
ordering and do not change application files. See the
[private project configuration decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261021T001732173Z-register-private-project-configuration.md).


`BACKLOT_PROJECT_CONFIG_DIR`, when present, selects the direct private registry
folder containing `projects.json` and `projects/`. Use an absolute external path;
empty, relative or unsafe selections fail without default fallback. When absent,
selection remains `os.UserConfigDir()/backlot`. Existing explicitly selected roots must be private
owned directories even before enrollment; default machine-only folders without
a registry retain their existing behavior. This does not select machine configuration
or change HOME/XDG or application pass-through. `version --json` reports
`state_version` so adapters can check support without resolving configuration.

## Shared-service declarations

Declare a bundled MariaDB or PostgreSQL service with an already-present local Engine
image; prefer an immutable digest for reviewed configuration.

```yaml
shared_services:
  sql:
    manager: mariadb
    image: mariadb:11.4
    storage: volume
    admin: generated
    port: 3306
    readiness_timeout: 60s
    # Optional direct server arguments; exact order/configuration is retained.
    args: [--max-connections=100]
resources:
  database: {kind: allocation, shared_service: sql}
environments:
  database:
    assign:
      DB_HOST: {ref: {kind: resource, name: database, field: host}}
      DB_PORT: {ref: {kind: resource, name: database, field: port}}
      DB_DATABASE: {ref: {kind: resource, name: database, field: database}}
      DB_USERNAME: {ref: {kind: resource, name: database, field: username}}
      DB_PASSWORD: {ref: {kind: resource, name: database, field: password}}
```

Select `database` in scene `resources` and each consuming component's `resources`;
include the reusable environment set explicitly with `environment_sets: [database]`.
The server is selected only through allocation resources. Every allocation field
stays symbolic offline, except automatically redacted `password`. Administrative
credentials have no application reference. Existing resource kinds cannot name a
`shared_service`. Migrations/fixtures remain ordinary dependent project jobs.
Unselected declarations require no Docker contact; planning never provisions
a server or allocation.

The declaration requires `manager: mariadb` with `port: 3306`, or
`manager: postgresql` with `port: 5432`, a nonempty `image`,
`storage: volume`, `admin: generated` and a positive
`readiness_timeout` up to 24h. Unknown fields remain errors. Native and
container environments resolve the actual consumer's connection context, described
in [shared runtime operation](containers.md#project-scoped-shared-services).

The [PostgreSQL example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/shared-postgresql/README.md) uses `postgres:17`.
PostgreSQL application roles can create their own schemas and trusted extensions
inside their allocation database, without database ownership or server/peer access.
Image/storage/authentication compatibility is verified, with evidence scoped to the
versions actually exercised; this declaration is not an automatic upgrade policy.

## Execution-scoped build images

`build_images` maps names to `{producer: build}`. The producer must be a native
`each-start` job and explicitly assign `{ref: {kind: build_image, name: app,
field: tag}}` to its environment. A container selects `{ref: {kind: build_image,
name: app, field: id}}` as its `image` and directly depends on that producer with
`condition: completed`; scenes explicitly select both. These references are only
for producer environment and container images, not fixtures, probes or general
outputs. A selected producer tag must have a selected image consumer. Reusable
sets follow ordinary environment layering; the tag must remain in the effective
producer environment. Missing producers, wrong owners/fields, dependencies and
cycles fail validation before effects.

The [runnable example](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/examples/build-images/README.md) builds this checkout
using project Docker tooling. The tag is unique to an instance, attempt and image
name. Successful completion and native join precede inspection at the configured
local Engine, followed by durable immutable-ID acceptance before consumer creation.
The producer must tag the supplied name even when caching avoids a rebuild;
failed/cancelled/missing/stale handoffs never fall back to an earlier result.
Align project tooling with machine `docker.endpoint`; Backlot adds no Dockerfile
engine, credentials, shared-input updates, automatic pulls/builds or image cleanup.

Offline planning preserves symbolic references and selected declarations without
running commands or looking up images. Valid unselected build-image declarations
do not contact Docker. Existing literal/input images keep their existing behavior.
See [retained ID and restart rules](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/containers.md#execution-scoped-image-retention)
and the [decision](https://github.com/octacian/backlot/blob/6177c64f60654657a914857edbffb69b8316a41b/docs/adr/20261025T202510851Z-pin-execution-scoped-build-images-from-native-jobs.md).
