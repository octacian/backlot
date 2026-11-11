---
title: "Compose bounded declared values for explicit consumers"
date: 2026-11-04
status: superseded
implementation: implemented
summary: "Compose explicit values without shell evaluation, losing allocation provenance or widening secret disclosure."
tags:
  - manifest
  - planning
  - security
  - compatibility
supersedes: []
superseded_by: [/docs/adr/20261111T202028571Z-allow-bounded-declared-value-composition-in-fixtures.md]
related: [/docs/adr/20261007T055849866Z-define-versioned-offline-scene-planning-contracts.md, /docs/adr/20261103T174849392Z-select-bounded-working-directories-for-native-commands.md, /docs/adr/20261022T201438827Z-retain-cumulative-daemon-state-migrations-from-version-9.md]
tracking: ["https://github.com/octacian/backlot/issues/109"]
implemented_by: ["https://github.com/octacian/backlot/pull/122"]
---

# Compose bounded declared values for explicit consumers

## Context and Problem Statement

Applications need wrappers solely to assemble allocated endpoints, canonical
origins with application paths, filesystem paths and command arguments. Whole-value
references cannot express these combinations. Shell snippets retain the plumbing and can
lose argument boundaries or disclose credentials. Allocation-free planning must
still describe unresolved values without inventing endpoints.

## Considered Options

- Extend the existing typed value contract with bounded string formatting,
  host/port construction, URL path appending and filesystem path joining.
- Keep application wrappers: no contract change, but retains the adapter burden.
- Add only concatenation: smaller syntax, but makes every application handle IPv6
  brackets, safe URL path appending and consumer-specific filesystem joining itself.
- Use concatenation alongside endpoint constructors: avoids placeholder validation,
  but interleaves punctuation and operands rather than expressing the chosen string
  layout together.
- Add shell/environment templates or an expression engine: more language and
  evaluation rules than these endpoint and argument combinations require.

## Decision Outcome

Keep existing literal/reference values and scalar string arguments. Add exactly
one of `format: {template: "...", values: [Value, ...]}`,
`host_port: {host: Value, port: Value}`,
`url_path: {base: Value, parts: [Value, ...]}` or
`file_path: {base: Value, parts: [Value, ...]}` as a value's alternative payload,
retaining optional `secret: true`. Operands use the same value contract recursively
within the existing document size/depth bounds. Both path constructors require `base` and
an explicit `parts` array, allowing `[]`; the unshipped URL `path` field has no alias.
Each new composition additionally has a 1 MiB rendered-output byte limit: use
checked length accounting before allocating or concatenating its output, enforcing the limit on known offline output and on
hydrated output before use, with sanitized errors. Existing literal/reference
values and scalar string arguments retain their compatibility without this new
limit. Size checks account for cleaning and boundary normalization; partially known
operands must not be rejected merely because their raw lengths exceed the final
output bound when later operands can shorten it.

Formatting supports only `%s`, substituting the next resolved string operand, and
`%%`, emitting a literal percent without consuming an operand. Require exactly one
operand per `%s`; reject other verbs, flags, widths, precisions, indexes and
incomplete percent sequences with sanitized errors. Preserve literal characters
and operand contents exactly, without reinterpreting substituted percent signs,
numeric conversion or automatic URL/credential escaping. Endpoint constructor
results can be operands; there is no `concat` alternative or expression engine.
This keeps the string layout together while retaining explicit typed operands.

Host/port construction validates
a hostname or numeric IP and numeric port in 1..65535, using `net.JoinHostPort`
for IPv6-safe joining.
Add an origin `hostname` reference for its canonical hostname without scheme or
port; origin attachment, selection and publication requirements remain.

Use Go's `net/url` for URL parsing, escaping and construction, adding only the
bounded append and rejection validation below, without custom parsing or encoding.
URL path composition accepts an absolute HTTP(S) base without credentials, query
or fragment. Append parts in order: each nonempty part appends beneath the
current base path with exactly one separator at the boundary; a single leading
slash does not replace that path. Empty parts are no-ops; `parts: []` preserves
the base unchanged while still validating it. Preserve other path characters and
each appended part's trailing slash until a subsequent nonempty append normalizes
the next boundary. Reject traversal, including recursively encoded dot segments or
separators, backslashes, authority replacement, and query/fragment syntax rather
than cleaning or interpreting parts as URL reference resolution.

Filesystem path composition applies ordinary Go joining and cleaning to `base`
followed by all `parts`: `filepath.Join` for native consumers and POSIX `path.Join`
for container consumers, regardless of daemon OS. Empty `parts` still joins and
normalizes the base. Allow `..`, including escape from the initial base, and use
the selected library's separator and cleaning semantics; reject NUL in every
operand. Do not borrow URL traversal or escaping rules. This constructs a string
without filesystem access, containment or authority, mount/path rewriting, or
implicit conversion of existing native paths to container paths. Working-directory,
mount and output policies remain unchanged. A filesystem-looking construction in
a URL or readiness context still must pass that context's final target validation.

Support compositions in explicit environment assignments, individual native and
container argv entries, and supported TCP/HTTP readiness targets. An argv entry
is either its existing literal string or a value object; resolution produces
exactly one argument without splitting, quoting or interpolation. Forwarded CLI
arguments remain literal strings. Host-native command readiness uses the same
argv contract, including on container services. Preserve its native environment
resolution and the accepted working-directory defaults and overrides. Existing
image, build-image and fixture context restrictions are not expanded.

Validate every operand's namespace, attachments, sensitivity and permitted context
before effects, including operands hidden by later environment overrides or
discarded by later path cleaning. Validate known construction inputs offline and
final destinations after hydration, before use. Partially resolve known operands while retaining unresolved typed composition
and reference provenance; offline planning performs no allocation or provider call.
Use the actual consumer's path syntax when known; otherwise retain symbolic
construction context rather than folding filesystem paths with host semantics.
Do not depend on environment assignment order or introduce environment references.

Resolve each operand for the actual consumer: native commands and command probes
use host endpoints, containers use their existing private/reachable endpoints,
and daemon network probes use their owned host destinations. Keep CA paths limited
to explicit environment assignments, including nested operands, selecting the host
public root or that container's declared read-only mount. Composition grants no
trust, mount, listener or publication-cycle exemption. Retain native listener and
container binding ownership checks and aggregate publication identity/TLS checks;
an aggregate composed target must retain the selected published origin. Track
ports consumed through every operand, including argv, before permitting retries.

Sensitivity is the union of the outer value and every operand. Reject sensitive
argv, including nested secret references, before launch. Redact an entire sensitive
composition in ordinary plans; retain its private operands through existing secret
capture. Keep hydrated secrets and secret-bearing errors out of ordinary diagnostics
and persisted public results, preserving existing explicit disclosure boundaries.

Read historical string argv and literal/reference snapshots without rewriting
them; preserve existing string encoding and equivalent literal-argument semantics
in compatibility comparisons. Use existing snapshot/attempt acceptance and drift
fences, including fresh-only initialization, mounted container arguments and
sensitive configuration. Healthy work keeps its accepted values and reports drift.
The additive representation requires no state migration if historical decoding
remains compatible; any demonstrated format break follows the migration policy.

### Consequences

Projects can remove assembly wrappers while application commands and settings
remain project-owned. Existing API, manifest, planner and daemon paths must share
operand traversal, context validation and redaction; direct-reference-only checks
would miss prerequisites, trust requirements, consumed ports or retained drift.
The scalar/object argv union also reaches strict decoding, editor schemas and
private snapshot readers. This adds no endpoint, store, dependency, background work,
runtime detection, image-building behavior or task execution engine. When unused,
existing lifecycle work and literal behavior remain unchanged.

The [fixture composition decision](/docs/adr/20261111T202028571Z-allow-bounded-declared-value-composition-in-fixtures.md)
replaces only the fixture composition exclusion. All other choices remain in force.
