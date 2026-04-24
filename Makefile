BINARY := diffguard
PKG    := ./cmd/diffguard
PATHS  := internal/,cmd/

# Shared env for evaluation suites. CI=true nudges sub-commands (cargo,
# npm) into non-interactive modes; CARGO_INCREMENTAL=0 keeps the
# mutation runs deterministic and avoids a multi-GB incremental cache.
EVAL_ENV := CI=true CARGO_INCREMENTAL=0

.PHONY: all build install test coverage check check-mutation check-fast eval-ts clean help

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

check-fast: build ## Run the full quality gate with sampled mutation testing (~20%)
	./$(BINARY) --mutation-sample-rate 20 --paths $(PATHS) .

check: build ## Run the full quality gate including 100% mutation testing (slow)
	./$(BINARY) --paths $(PATHS) .

check-mutation: build ## Only the mutation section, full codebase
	./$(BINARY) --paths $(PATHS) --fail-on warn .

eval-ts: ## Run the TypeScript correctness eval (EVAL-3). Requires node+npm for mutation tests.
	$(EVAL_ENV) go test ./internal/lang/tsanalyzer/... -run TestEval -count=1 -v

clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.out

help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
