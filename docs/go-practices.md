# Go practices

## Packages and files

Group substantial systems by domain or responsibility. Keep executable entry
points under `cmd/`, implementation packages under `internal/`, and the shared
versioned wire contract under `api/`. Introduce packages as real behavior arrives.
Avoid broad `models`, `services`, `helpers`, or `utils` dumping grounds.

Within a package, group a type and its related behavior together. Split files
when responsibilities become distinct; do not split every method into its own
file. Match existing naming, errors, and public/private boundaries. Constructors
should make validation and side effects apparent; keep in-memory validation
separate from external resource allocation where practical.

Keep CLI actions and HTTP handlers thin. Put lifecycle decisions in package
APIs reusable by those adapters. Share contracts instead of copying struct
definitions or maintaining parallel field lists.

## Reuse and interfaces

Follow the [dependency policy](contributing.md). Prefer existing helpers and
libraries to a new implementation. Introduce a helper for a repeated invariant
or meaningful operation, not simply to shorten a line.

Accept the narrowest interface the consumer needs. Define interfaces near the
consumer when substitution is useful. Do not wrap every library in a mirrored
interface or introduce a provider framework for hypothetical implementations.
Keep persistence and operating-system boundaries explicit enough to exercise
failure and recovery behavior.

## Errors and concurrency

Return errors with useful operation context and preserve causes with `%w`.
Use `errors.Is` and `errors.As` for classification. Keep test exit status,
infrastructure failure, collection failure, and cleanup failure distinguishable.
Do not silently convert partial failures into success.

Pass `context.Context` explicitly into cancellable operations. Background work
must have an owner, cancellation path, and join/shutdown path. Use established
coordination primitives such as `errgroup` when appropriate. Avoid untracked
goroutines. Stop consumers before releasing resources they can still access;
honor a defined shutdown budget and report unfinished work.

## Documentation

Document all exported functions, types, variables, and constants with GoDoc
comments beginning with the identifier and using complete sentences. Document
unexported helpers when their preconditions, side effects, or behavior are not
obvious. Explain intent, invariants, ownership, and surprising ordering in complex
code; avoid narrating syntax.

Package documentation should explain a meaningful boundary when that is not
clear from its name. Keep public behavior, error cases, and cancellation
semantics current when implementations change.

## Shared contracts

Follow [the shared API contract](architecture.md#shared-api). Both client and
server marshal/unmarshal the same named Go types. Validate decoded values;
static Go typing does not validate untrusted JSON. Avoid `map[string]any` and
anonymous structs as endpoint contracts. Keep internal persistence and secret
values out of wire types unless the endpoint explicitly exposes them.
