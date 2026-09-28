# Physical Security Intelligence Layer — M1
#
# Toolchain paths are overridable so the same Makefile works on a developer
# machine, on CI, and on an air-gapped build host.
GO            ?= go
PROTOC        ?= protoc
PROTOC_GEN_GO ?= protoc-gen-go
GOBIN         ?= $(shell go env GOPATH)/bin

CONTRACTS_DIR := contracts/protobuf
CONTRACT_BASELINE := contracts/compat/baseline.json
MODULE := github.com/Sergey-Bar/Ai-Captain
MIGRATIONS_DIR := migrations

.PHONY: all bootstrap generate contract-check build test test-race lint fmt vet \
        tidy migrate-up migrate-status rules-lint ci clean

all: build test

bootstrap:
	$(GO) install $(PROTOC_GEN_GO)@latest
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Contracts are the source of truth. Nothing is hand-written under
# internal/contracts/ except this rule's output.
generate:
	$(PROTOC) -I $(CONTRACTS_DIR) \
		--go_out=. --go_opt=module=$(MODULE) \
		$(CONTRACTS_DIR)/security/v1/*.proto
	$(GO) run ./tools/contractcheck -mode check -baseline $(CONTRACT_BASELINE) -root $(CONTRACTS_DIR)

# The gate: a breaking contract change fails the build. Run with -mode write only
# when the change is intentionally a new contract version.
contract-check:
	$(GO) run ./tools/contractcheck -mode check -baseline $(CONTRACT_BASELINE) -root $(CONTRACTS_DIR)

contract-baseline:
	$(GO) run ./tools/contractcheck -mode write -baseline $(CONTRACT_BASELINE) -root $(CONTRACTS_DIR)

build:
	$(GO) build -trimpath -o bin/ ./cmd/...

# -p 1: the database-backed suites share one database and rebuild the schema, so
# package test binaries must not run concurrently. Without it, one suite's
# DROP SCHEMA lands in the middle of another's test and produces failures that
# look like product bugs.
test:
	$(GO) test -p 1 ./...

test-race:
	$(GO) test -p 1 -race -count=1 ./...

lint:
	golangci-lint run ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

tidy:
	$(GO) mod tidy

migrate-up:
	$(GO) run ./cmd/security-migrate up

migrate-status:
	$(GO) run ./cmd/security-migrate status

# Rule bundles are untrusted input. They are linted on every change, in CI,
# with no Milestone server available. The engine is in place; simulate and
# activate land with the rest of Track D.
rules-lint:
	$(GO) run ./cmd/securityctl rules lint --bundle rules/builtin

# Every migration filename must carry an expand/backfill/contract phase. A file
# without one is not a migration this runner will apply, and silently skipping it
# is how a schema and a binary drift apart.
migration-phases:
	@$(GO) test -p 1 ./migrations/... -run TestAllLoadsAndValidates -count=1

# The published bundle schema and the embedded copy must be the same file. The
# compiler enforces the embed, so a stale copy is a bundle validated against a
# contract nobody published.
contract-sync:
	@cmp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/rule-bundle.schema.json \
		|| (echo "internal/rules/schema is stale; copy the published contract:"; \
		    echo "  cp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/"; exit 1)

ci: contract-check contract-sync vet lint test-race migration-phases rules-lint

clean:
	rmdir /s /q bin 2>nul || true
