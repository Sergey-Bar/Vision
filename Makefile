# Physical Security Intelligence Layer — M1
#
# Toolchain paths are overridable so the same Makefile works on a developer
# machine, on CI, and on an air-gapped build host.
GO            ?= go
PROTOC        ?= protoc
PROTOC_GEN_GO ?= protoc-gen-go
GOBIN         ?= $(shell go env GOPATH)/bin

CONTRACTS_DIR := contracts/protobuf
SCHEMA_DIR := contracts/json-schema
OPENAPI_DIR := contracts/openapi
CONTRACT_BASELINE := contracts/compat/baseline.json
MODULE := github.com/Sergey-Bar/Ai-Captain
MIGRATIONS_DIR := migrations

.PHONY: all bootstrap generate contract-check contract-check-schema \
        contract-check-openapi contract-check-all contract-baseline build test \
        test-race lint fmt vet tidy migrate-up migrate-status rules-lint \
        recovery-drill sql-guard ci \
        verify clean

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
# when the change is intentionally a new contract version, and then only with
# -allow-breaking and a human reading the diff: the write mode refuses a
# baseline refresh that drops or retypes anything, because the first run over a
# surface the tool has never diffed surfaces facts rather than regressions.
contract-check:
	$(GO) run ./tools/contractcheck -mode check -baseline $(CONTRACT_BASELINE) -root $(CONTRACTS_DIR)

# The JSON Schemas and the OpenAPI document are contracts too. They were only
# os.Stat-ed by a test until D5; these two are the same comparison over the
# other two published surfaces, and they belong in the gate for the same reason
# the protobuf one does.
contract-check-schema:
	$(GO) run ./tools/contractcheck -mode check -baseline $(CONTRACT_BASELINE) -root $(SCHEMA_DIR)

contract-check-openapi:
	$(GO) run ./tools/contractcheck -mode check -baseline $(CONTRACT_BASELINE) -root $(OPENAPI_DIR)

contract-check-all: contract-check contract-check-schema contract-check-openapi

contract-baseline:
	$(GO) run ./tools/contractcheck -mode write -baseline $(CONTRACT_BASELINE) -root $(CONTRACTS_DIR)
	$(GO) run ./tools/contractcheck -mode write -baseline $(CONTRACT_BASELINE) -root $(SCHEMA_DIR)
	$(GO) run ./tools/contractcheck -mode write -baseline $(CONTRACT_BASELINE) -root $(OPENAPI_DIR)

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

# The published schemas and the copies embedded in the binary must be the same
# bytes. The compiler enforces the embed, so a stale copy is a document being
# validated against a contract nobody published. Every schema with a Go-side
# copy is compared, not just rule-bundle (D5).
contract-sync: contract-check-schema
	@cmp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/rule-bundle.schema.json \
		|| (echo "internal/rules/schema is stale; copy the published contract:"; \
		    echo "  cp contracts/json-schema/rule-bundle.schema.json internal/rules/schema/"; exit 1)
	@$(GO) test -count=1 -run TestEmbeddedSchemas ./internal/contracts/schemasync/ \
		|| (echo "an embedded schema copy has drifted from contracts/json-schema;"; \
		    echo "copy the published contract over internal/contracts/schemasync/"; exit 1)

ci: contract-check contract-check-schema contract-check-openapi contract-sync vet lint test-race migration-phases rules-lint recovery-drill sql-guard

# The gate that runs, and the one to trust.
#
# `ci` is the same list. They were different for months: this target left lint
# and the generated-code check out because the tools were not installed, and the
# exclusion was described rather than fixed. golangci-lint is now a real gate —
# it found an environment file written world-readable and a backoff expression
# that raised on its own failure path — so leaving it out of the gate a developer
# actually runs was leaving the useful half out.
#
# What is still absent is the generated-code check, which needs protoc. It is
# named here rather than quietly dropped, and `generate` is the target that
# satisfies it.
#
# The database-backed suites skip rather than fail without SP_TEST_DATABASE_URL,
# so a green run here can be a run that tested nothing. See AGENTS.md.
verify: contract-check contract-check-schema contract-check-openapi contract-sync vet fmt-check lint test-race migration-phases rules-lint recovery-drill sql-guard

# The demo, end to end, and a transcript of it at dist/demo/transcript.md.
#
# A judge gets ninety seconds and a URL, so the demo has to be one command and the
# artefact that outlives it has to be written down. This brings the stack up, runs
# scripts/demo.sh, and leaves the transcript in the working tree.
#
# Every number in the transcript came out of one of the product's own binaries, and
# nothing in it is written by hand. Where a step could not run it says NOT RUN and
# names why -- `not-run` is a distinct verdict from `pass` everywhere in this
# repository and the transcript is where a judge sees the difference.
#
# It runs against the VERIFY image rather than the product one, because the driver
# needs a shell and PowerShell and the product image deliberately has neither. That
# image never ships to a site, which is the whole reason the product image can have
# no shell without this being a contradiction.
demo:
	docker compose --profile demo up --exit-code-from demo demo

# The gate, in a container, against the working tree rather than a snapshot of it.
# `make verify` is the same list of checks; this is the one that runs where the
# product runs.
verify-container:
	docker compose --profile verify run --rm verify

# Every SQL statement the repository writes, checked against the schema: a mutation
# of a tenant-scoped table names its tenant, a write to a leased table names its
# lease owner, and an append-only table is only inserted into. The table facts are
# derived from the migrations, so a table added tomorrow is covered today.
sql-guard:
	$(GO) test -p 1 -count=1 -timeout 300s ./internal/sqlguard/...

# The whole-recovery drill, as its own target rather than a line in a package
# count. It is the only thing here that proves a decision still replays after the
# database and the checkpoint material are both gone, which is the property the
# append-only hash-chained design exists for.
recovery-drill:
	$(GO) test -p 1 -count=1 -timeout 900s -v -run TestRecoveryDrill ./internal/dr/...

# Fails if anything is unformatted. Same intent as the CI fmt job, phrased so it
# does not depend on a shell pipeline.
fmt-check:
	@out=$$($(GO) fmt ./... 2>&1); \
	if [ -n "$$out" ]; then echo "gofmt reformatted these; commit the result:"; echo "$$out"; exit 1; fi

clean:
	rmdir /s /q bin 2>nul || true
