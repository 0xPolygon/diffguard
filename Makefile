BINARY := diffguard
PKG    := ./cmd/diffguard
PATHS  := internal/,cmd/

.PHONY: all build install test coverage check check-mutation check-fast clean help

all: build

build: ## Build the diffguard binary
	go build -o $(BINARY) $(PKG)

install: ## go install the binary to GOBIN
	go install $(PKG)

test: ## Run all unit tests
	go test ./... -count=1

coverage: ## Generate coverage.out and print the per-package summary
	go test ./... -coverprofile=coverage.out -covermode=atomic
	@go tool cover -func=coverage.out | tail -1

check-fast: build ## Run complexity / size / deps / churn against the whole tree (skips mutation)
	./$(BINARY) --skip-mutation --paths $(PATHS) .

check: build ## Run the full quality gate including mutation testing (slow)
	./$(BINARY) --paths $(PATHS) .

check-mutation: build ## Only the mutation section, full codebase
	./$(BINARY) --paths $(PATHS) --fail-on warn .

clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
