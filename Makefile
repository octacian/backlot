GOLANGCI_LINT_VERSION := v2.14.0
RELEASE_GOOS ?= darwin
RELEASE_GOARCH ?= arm64

LINT := .tools/bin/golangci-lint-$(GOLANGCI_LINT_VERSION)

.PHONY: build release-build notices notices-check fmt fmt-check vet test race tools lint adr-create adr-verify adr-test schema-generate schema-check check

build:
	go build -o bin/backlot ./cmd/backlot

# Fixed toolchain/module sources, no local paths, VCS dirtiness or wall clock.
release-build: notices-check
	CGO_ENABLED=0 GOOS=$(RELEASE_GOOS) GOARCH=$(RELEASE_GOARCH) go build -trimpath -buildvcs=false -ldflags='-buildid=' -o bin/backlot-$(RELEASE_GOOS)-$(RELEASE_GOARCH) ./cmd/backlot

notices:
	go run ./cmd/notices

notices-check:
	go run ./cmd/notices -check

fmt:
	go fmt ./...

fmt-check:
	@unformatted=$$(gofmt -l $$(git ls-files --cached --others --exclude-standard '*.go')); \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

# The native integration suite builds an external CLI; its sources are not
# dependencies of Go's cached parent test binary. Always execute this gate.
race:
	go test -race -count=1 ./...

tools: $(LINT)

$(LINT):
	mkdir -p .tools/bin
	GOBIN="$(CURDIR)/.tools/bin" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv .tools/bin/golangci-lint $(LINT)

lint: tools
	$(LINT) config verify
	$(LINT) run

adr-create:
	go run ./cmd/adr create "$(TITLE)"

adr-verify:
	go run ./cmd/adr verify

adr-test:
	go test ./internal/adr ./cmd/adr

schema-generate:
	go run ./cmd/schema

schema-check:
	go run ./cmd/schema -check

check: schema-check notices-check fmt-check vet lint race adr-verify build
