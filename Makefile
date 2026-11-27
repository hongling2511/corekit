# Unified build entry for corekit. CI runs these same targets.

# Pinned tool versions; CI and local runs install exactly these into ./bin.
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.10.0

BIN           := $(CURDIR)/bin
GOLANGCI_LINT := $(BIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)/golangci-lint
GOVULNCHECK   := $(BIN)/govulncheck-$(GOVULNCHECK_VERSION)/govulncheck

.PHONY: build test lint gen run docker tools

build:
	go build ./...

test:
	go test -race ./...

# golangci-lint runs the linters (including depguard layering rules) and the
# gofumpt formatter check; govulncheck scans for known vulnerabilities.
lint: $(GOLANGCI_LINT) $(GOVULNCHECK)
	$(GOLANGCI_LINT) run ./...
	$(GOVULNCHECK) ./...

gen:
	go generate ./...

# Runs the sample service (its HTTP API on http.addr, default :8080) until SIGINT or SIGTERM.
run:
	go run ./cmd/sample serve

# No image yet; the Dockerfile arrives in a later stage.
docker:
	@echo "docker: no image to build yet"

tools: $(GOLANGCI_LINT) $(GOVULNCHECK)

$(GOLANGCI_LINT):
	GOBIN=$(dir $@) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

$(GOVULNCHECK):
	GOBIN=$(dir $@) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
