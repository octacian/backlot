GOLANGCI_LINT_VERSION := v2.14.0
LINT := .tools/bin/golangci-lint-$(GOLANGCI_LINT_VERSION)

.PHONY: build fmt fmt-check vet test race tools lint adr-create adr-verify adr-test check

build:
	go build -o bin/backlot ./cmd/backlot

fmt:
	go fmt ./...

fmt-check:
	@unformatted=$$(gofmt -l $$(git ls-files --cached --others --exclude-standard '*.go')); \
	if [ -n "$$unformatted" ]; then echo "$$unformatted"; exit 1; fi

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

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

check: fmt-check vet lint race adr-verify build
