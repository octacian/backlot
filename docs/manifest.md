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

## Editor completion and validation

Use the committed [JSON Schemas and editor setup](../schemas/README.md) for
project Manifest and MachineConfig YAML/JSON completion, hover help and local
validation. YAML in VSCode requires the Red Hat YAML extension. The example
modelines resolve to local schemas in this checkout before merge; no runtime
schema fetch is required. Editor schemas assist authoring; the strict decoder
and `backlot plan` remain authoritative.

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

`inputs` maps names to `{secret: true}` or `{}`, optionally with descriptions
and secret generation hints. Referenced inputs must have nonempty effective
values in registered private project `inputs`. Secret inputs remain redacted in results.
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
Overlapping paths must share a group, including canonical aliases through
symlinks and missing suffixes, across scene declarations. Selected existing output
ancestors cannot escape the project through symlinks. Components list their
outputs. A future executor must serialize these groups per canonical checkout
across scenes and runs; a plan is not a lock. Backlot cannot infer undeclared
command side effects.

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
symbolic origin or literal HTTP(S) URL without credentials. Explicit
HTTP(S) ports, including Caddy admin endpoint ports, must be in 1..65535; omitted
ports retain the scheme default and an explicitly empty port is invalid. A symbolic
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

Repeated start and stop/run use actual retained selections/values despite current
manifest/config drift. Restart/reset adopt current selections through existing
compatibility and snapshot rules. A startup replaced before observed readiness
conflicts instead of reporting a replacement attempt's summary. Ordinary plans,
status/follow, inspect, diagnostics, fixture discovery, execution results and
exports remain redacted. Keep credentials out of labels/identifiers and protect
startup output as sensitive. See the [seeded example](../examples/macos-quickstart/README.md#seeded-demo-account)
and [decision](adr/20261022T195459793Z-authorize-declared-sensitive-startup-details-for-persistent-scenes.md).

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
| `input` | Declared input | Omit | Resolve registered private project value |
| `instance` | Omit | `project`, `checkout`, `scene` | Resolve known provenance |
| `instance` | Omit | `id` | Symbolic; no instance allocated |
| `resource` | Attached/selected resource | `port`, `path`, `name`, `value`, `url` according to kind | Symbolic; secret values redacted |
| `service` | Selected service | `host` or `port` (with named `port`) | Symbolic |
| `output` | Attached output | `path` | Resolve project-relative output path |

Resource fields are `port.port`, `directory.path`, `volume.name`, `network.name`,
`secret.value`, and `origin.url` or `origin.ca_cert_path`. The latter is only for
explicit component environment assignments (including explicitly selected reusable sets), requires
a selected/attached/published origin and explicit managed Caddy configuration,
and stays symbolic offline. Container assignments also require an explicit
`origin_ca_cert` mount and resolve to its target. See [explicit application trust](https.md#explicit-application-trust). Unused reference fields may be omitted or
empty strings; nonempty extra values are rejected. Prefer omission when authoring
manifests.
There are no concatenation, expressions, variable substitutions or embedded
`${...}` references. Literal strings retain their literal characters.

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

Components may also declare named `artifacts` and `fixtures`; see the
[disposable evidence contract](evidence.md#declared-artifacts-and-fixtures).

## Machine schema and results

Machine configuration uses the same version and may declare:

- `tools`: declared tool executable overrides.
- `docker`: `endpoint` as a local Unix socket URL (canonical `unix:///...`;
  `unix:/...` is also accepted), optional container-to-native `host_address` and
  numeric mapped-port `publish_address` (default `127.0.0.1`).
- `caddy`: HTTP(S) admin `endpoint` without credentials/query/fragment, owned
  identifier `scope`, concrete `domain_suffix`, gateway-reachable `host_address`
  and optional `https_port` (default 443). See [HTTPS setup and routing](https.md).
- `storage`: `directory`, positive duration `retention_age` and positive integer
  `retention_bytes`. These scene configuration settings remain validated intent. Configure the running
  daemon with `--state-dir`, `--retention-age` and `--retention-bytes`; scene input
  never changes the daemon-wide policy.

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
machine example contains illustrative endpoints; its private example supplies the image name.

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
`--reassociate` and new values; old credentials are not implicitly imported.
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
Running attempts retain private launch values and provenance. Repeated run reports
drift; explicit restart/reset resolves current layers subject to existing retained
resource safety checks. Stop, destroy and recovery use recorded snapshots.
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
[private project configuration decision](adr/20261021T001732173Z-register-private-project-configuration.md).


`BACKLOT_PROJECT_CONFIG_DIR`, when present, selects the direct private registry
folder containing `projects.json` and `projects/`. Use an absolute external path;
empty, relative or unsafe selections fail without default fallback. When absent,
selection remains `os.UserConfigDir()/backlot`. Existing explicitly selected roots must be private
owned directories even before enrollment; default machine-only folders without
a registry retain their existing behavior. This does not select machine configuration
or change HOME/XDG or application pass-through. `version --json` reports
`state_version` so adapters can check support without resolving configuration.
