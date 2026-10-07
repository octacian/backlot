# Editor schemas

The committed [project Manifest](manifest-v1.schema.json) and
[MachineConfig](machine-config-v1.schema.json) artifacts use JSON Schema
**Draft 2020-12**. Both describe `version: backlot/v1`. They are self-contained:
all references are internal, so editors can use local copies without fetching a
runtime schema. Their stable `$id` values reserve the eventual `main` raw-file
locations; those URLs are **not verified live before this branch merges**.

## YAML in VSCode and other editors

Install the [Red Hat YAML extension](https://github.com/redhat-developer/vscode-yaml)
(`redhat.vscode-yaml`) in VSCode. VSCode alone does not provide this YAML schema
support. The extension supplies schema-based completion, hover descriptions and
validation. [YAML Language Server](https://github.com/redhat-developer/yaml-language-server#readme)
documents Draft 2020-12 support and modeline paths relative to the YAML file.
The repository examples already associate their correct local schema:

```yaml
# yaml-language-server: $schema=../../schemas/manifest-v1.schema.json
version: backlot/v1
```

For a custom project, copy the appropriate schema to `.backlot/` and use
`# yaml-language-server: $schema=.backlot/manifest-v1.schema.json` in its root
manifest (or an appropriate path relative to a machine config). Alternatively,
add workspace settings yourself; relative `yaml.schemas` keys resolve from a
single-folder workspace root:

```json
{
  "yaml.schemas": {
    "./.backlot/manifest-v1.schema.json": ["**/backlot.yaml", "**/backlot.yml"],
    "./.backlot/machine-config-v1.schema.json": ["machine.example.yaml"]
  },
  "yaml.validate": true,
  "yaml.completion": true,
  "yaml.hover": true
}
```

Adjust globs for your files, and use the language server's documented folder-name
prefix for multi-root workspaces. A modeline takes priority over settings. Other
editors using YAML Language Server can use the same local modeline.

## JSON

JSON represents the same contracts. Associate the local schema through the
editor rather than adding a `$schema` data field, which Backlot's strict decoder
rejects. VSCode has [built-in JSON schema associations](https://code.visualstudio.com/docs/languages/json#_json-schemas-and-settings):

```json
{
  "json.schemas": [
    {"fileMatch": ["**/backlot.json"], "url": "./.backlot/manifest-v1.schema.json"},
    {"fileMatch": ["machine.json"], "url": "./.backlot/machine-config-v1.schema.json"}
  ]
}
```

## Generation and validation boundaries

Run `make schema-generate` at the repository root and commit the two resulting
JSON files. `make schema-check` compares deterministic output without writing;
`make check` and both CI platforms run this drift check. The field inventory,
required fields and fixed-object unknown-field rejection come from the named
[Go contracts](../api/v1/manifest.go). JSON Schema annotations live on those
contracts, and narrow [per-type hooks](../api/v1/manifest_schema.go) add local
relationships. Wire encoding and runtime validation are unchanged. Do not edit
generated JSON or introduce duplicate schema structs/field inventories.

Schemas catch missing/unknown fields, version/enums, declaration key patterns,
empty required selections, common value/reference/runtime/probe/mount shapes,
port bounds, and positive retention sizes. They deliberately cover a manageable
subset of semantic validation. `backlot plan` remains authoritative for graph
closure, declared references/resource kinds, cycles, overlapping paths and path
safety, positive Go duration values and deadline bounds, complete URL/domain
validity, executable availability, inputs, seed contents, and provider requirements.
Neither a schema nor offline planning proves provider health or execution.
JSON Schema validates parsed values: it cannot enforce duplicate-key rejection,
YAML tags/anchors/aliases/merge-key restrictions, trailing-document rejection or
Backlot's byte/depth bounds. Keep using the strict decoder and planner before
runtime work. Tests validate actual YAML examples and invalid local values with
a real schema validator; editor UI behavior was not observed during development.

## Library choice

Evaluated upstream source, release/module metadata, API, licenses and dependency
cost before adding dependencies:

- [Invopop/jsonschema v0.14.0](https://github.com/invopop/jsonschema/tree/v0.14.0)
  (MIT, `COPYING`; April 2026 release) reflects existing `json` tags, defaults
  fixed structs to `additionalProperties: false`, reads GoDoc, and offers
  `JSONSchemaExtend` hooks and 2020-12 output. This fits the source-of-truth rule
  without another generator/parser. It supports Go 1.24+, beneath our toolchain.
  Its production dependencies are `pb33f/ordered-map/v2`, `bahlo/generic-list-go`,
  `buger/jsonparser`, and `go.yaml.in/yaml/v4` (an ordered-map dependency, currently
  rc.2). That additional YAML module is transitive; Backlot's document parser
  remains the existing YAML v3. Hooks put the schema library in the shared API's
  dependency graph; no reflection/generation runs in the planner.
- [Swaggest/jsonschema-go v0.3.79](https://github.com/swaggest/jsonschema-go/tree/v0.3.79)
  (MIT; November 2025 release) is also maintained and supports Draft 7 reflection
  and interception. Its broader reflection/tag API provides no needed advantage
  here; Invopop's direct extension hooks and current draft are a smaller adapter.
  Module requirements include development/assertion/diff tooling as well as
  `swaggest/refl`; module requirements are not all production imports.
- [Santhosh Tekuri/jsonschema v6.0.3](https://github.com/santhosh-tekuri/jsonschema/tree/v6.0.3)
  (Apache-2.0; June 2026 release) provides standards validation and meta-schema
  checks for 2020-12. It is used by tests only, with resources loaded from bytes
  and no HTTP loader. Its production `x/text` dependency is already present;
  `dlclark/regexp2` is upstream test tooling.

Versions are pinned in `go.mod`. JSON and YAML share the generated artifacts;
no Node toolchain, custom schema parser, or runtime download is required.
