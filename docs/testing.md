# Testing and tooling

## Setup

Use the Go version declared in `go.mod` (currently 1.27.1), Git, and Make.
Repository tooling is Go-only; Node and a JavaScript package manager are not
required. Download module dependencies with `go mod download`.

`make tools` installs the pinned golangci-lint version into `.tools/bin` using
the current Go toolchain. This first build can take several minutes. It does
not change global tooling. Lint configuration lives in `.golangci.yml`.

## Commands

Run these from the repository root:

| Command | Purpose |
| --- | --- |
| `make build` | Build `bin/backlot` |
| `go run ./cmd/backlot version --json` | Inspect the scaffold's typed version result |
| `go run ./cmd/backlot plan test --project examples/adoption --json -- -run TestExample` | Validate an offline fixture plan and forward terminal arguments |
| `make fmt` | Format Go source |
| `make fmt-check` | Check Go formatting without editing |
| `make vet` | Run `go vet ./...` |
| `make test` | Run Go tests |
| `make race` | Run Go tests with the race detector |
| `make tools` | Install pinned development tooling locally |
| `make lint` | Validate lint configuration and run golangci-lint |
| `make adr-create TITLE="Decision title"` | Create a proposed ADR without overwriting records |
| `make adr-verify` | Verify all ADR records |
| `make adr-test` | Run ADR tooling tests |
| `make schema-generate` | Regenerate committed editor schemas from `api/v1` |
| `make schema-check` | Reject stale generated schemas without writing |
| `make check` | Run schema drift checks, formatting, vet, lint, race tests, ADR verification, and build |

Before committing scaffold or Go/tooling changes, run `make check`. Documentation
changes require ADR verification when records change and manual checking of
links, commands, and consistency with the product contract. Run additional
focused checks when changed behavior warrants them; do not add tests that merely
duplicate implementation details.

## CI and platform evidence

GitHub Actions runs the shared checks on macOS ARM64. Linux checks provide early
portability evidence, not a Linux runtime support claim. A Windows cross-build
checks compilation only, not process lifecycle, networking, or installation.
The workflow runs on every PR and push to `main`, without path filters that
could leave required checks missing.

Schema tests use a maintained JSON Schema validator on the real YAML examples
and negative/variant cases, in addition to checking deterministic generated output.
See [schema generation and editor setup](../schemas/README.md).

Tests exercise strict manifest/config decoding, graph/reference validity, offline
CLI JSON/errors, environment precedence, read-only seeds, secret redaction, tool
availability and ADR validation. They do not demonstrate a daemon, Docker
integration, HTTPS routing, or the V1 workflows.
The [implementation plan](implementation-plan.md) defines those later acceptance
checks. Runtime integration tests must use owned resources and must never modify
an operator's unrelated containers, routes, files, or databases.
