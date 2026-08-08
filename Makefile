CONTAINER_ENGINE?=docker
GOBIN:=$(shell go env GOPATH)/bin
export PATH:=$(GOBIN):$(PATH)

# .env (container-network hostnames, e.g. DB_DSN@postgres:5432) is what
# docker-compose's `app` service loads via `env_file:`. .env.local (localhost
# hostnames, e.g. DB_DSN@localhost:5433) is what this Makefile loads for anything
# run directly on the host (`make run`, `make migrate-up`) against the same
# docker-compose Postgres — two files because the app and the host need different
# hostnames for the same database.
ifneq (,$(wildcard ./.env.local))
include .env.local
export
endif

.PHONY: build docker-build deps lint lint-fix run mocks \
        test-unit test-integration test \
        deps-up deps-down monitor-up monitor-down up down load-test load-test-seed \
        new-migration migrate-up migrate-down clean

build: clean deps
	go build -o ./out/app ./cmd/server

docker-build:
	$(CONTAINER_ENGINE) build -t qonto-bulk-transfer . --progress=plain

# Tool versions pinned inline here — mise.toml covers `go` itself; these are
# `go install`-able CLI tools instead.
deps:
	@go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.3
	@go install github.com/vektra/mockery/v2@v2.53.6
	@go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.19.1
	@go install github.com/gotesttools/gotestfmt/v2/cmd/gotestfmt@v2.5.0
	@go install github.com/tsenart/vegeta@latest
	@go mod download

lint:
	golangci-lint run --verbose --timeout 10m

lint-fix:
	golangci-lint run --fix --verbose

run:
	go run ./cmd/server

mocks: deps
	mockery

# gotestfmt turns `go test -json` output into a readable pass/fail summary.
test-unit:
	go test -race -short -count=1 -failfast -json -v ./... | gotestfmt

# Brings up the "testing" profile's Postgres (see docker-compose.yml), runs the
# integration-tagged suite, and always tears the stack back down afterward — even
# on failure, so `make test` never leaves a container running.
test-integration: deps-up
	@go test -race -count=1 -failfast -json -v -tags=integration ./... | gotestfmt; \
	ret=$$?; $(MAKE) deps-down; exit $$ret

test: deps test-unit test-integration

deps-up:
	$(CONTAINER_ENGINE) compose --profile testing up -d --wait --wait-timeout 60

deps-down:
	$(CONTAINER_ENGINE) compose --profile default --profile testing --profile monitor down -v --remove-orphans --timeout=3

monitor-up:
	$(CONTAINER_ENGINE) compose --profile monitor up lgtm -d
	@echo "LGTM stack started:"
	@echo "  - Grafana:    http://localhost:3000"
	@echo "  - OTLP HTTP:  http://localhost:4318"
	@echo "  - OTLP gRPC:  http://localhost:4317"

monitor-down:
	$(CONTAINER_ENGINE) compose --profile monitor down lgtm

# Whole system, traces included: postgres + app + the lgtm stack together (the
# `default` and `monitor` profiles up at once), so OTEL_EXPORTER_OTLP_ENDPOINT
# (pointed at lgtm:4317 in docker-compose.yml's app environment) actually has
# somewhere to send to.
up:
	DOCKER_BUILDKIT=0 COMPOSE_DOCKER_CLI_BUILD=0 $(CONTAINER_ENGINE) compose --profile default --profile monitor up -d --build --wait --wait-timeout 60
	@echo "System up:"
	@echo "  - App:      http://localhost:8080"
	@echo "  - Grafana:  http://localhost:3000  (dashboard: Bulk Transfer Service)"

down:
	$(CONTAINER_ENGINE) compose --profile default --profile monitor down -v --remove-orphans --timeout=3

# Fires LOAD_TEST_REQUESTS requests at both endpoints, round-robin across
# LOAD_TEST_ACCOUNTS distinct seeded accounts (see
# scripts/seed-load-test-accounts.sql) — `make up` first. Spreading writes
# across many well-funded accounts instead of one shared one means most POSTs
# succeed by design, still with real concurrent write throughput (several
# requests per account in flight at once) rather than every request past the
# first failing outright. Tune LOAD_TEST_TRANSFER_CENTS/
# LOAD_TEST_BALANCE_CENTS/LOAD_TEST_ACCOUNTS lower/fewer to deliberately
# induce contention and see 422s again.
#
# Idempotency-Key is prefixed with a fresh run ID every invocation — reusing
# key strings across separate load-test runs would replay a *previous* run's
# recorded outcomes instead of evaluating fresh requests (idempotency_keys is
# a global, content-addressed ledger; this bit us once already).
LOAD_TEST_ACCOUNTS?=20
LOAD_TEST_REQUESTS?=1000
LOAD_TEST_RATE?=50
LOAD_TEST_DURATION?=10s
LOAD_TEST_BALANCE_CENTS?=100000000
LOAD_TEST_TRANSFER_CENTS?=1000

load-test-seed: up
	$(CONTAINER_ENGINE) compose exec -T postgres psql -U qonto -d qonto \
		-v accounts=$(LOAD_TEST_ACCOUNTS) -v balance_cents=$(LOAD_TEST_BALANCE_CENTS) \
		< scripts/seed-load-test-accounts.sql

load-test: deps load-test-seed
	@account_ids=$$(mktemp); post_targets=$$(mktemp); get_targets=$$(mktemp); \
	$(CONTAINER_ENGINE) compose exec -T postgres psql -U qonto -d qonto -t -A \
		-c "SELECT id FROM bank_accounts WHERE bic LIKE 'LOADTST%' ORDER BY bic" > "$$account_ids"; \
	go run ./cmd/loadtestgen \
		--run-id "$$(date +%s)" \
		--requests $(LOAD_TEST_REQUESTS) --accounts $(LOAD_TEST_ACCOUNTS) \
		--account-ids-file "$$account_ids" --transfer-cents $(LOAD_TEST_TRANSFER_CENTS) \
		--post-out "$$post_targets" --get-out "$$get_targets"; \
	echo "=== POST /transfers/bulk ==="; \
	vegeta attack -format=json -targets="$$post_targets" -rate=$(LOAD_TEST_RATE) -duration=$(LOAD_TEST_DURATION) | vegeta report; \
	echo "=== GET /accounts/{id}/transactions ==="; \
	vegeta attack -format=json -targets="$$get_targets" -rate=$(LOAD_TEST_RATE) -duration=$(LOAD_TEST_DURATION) | vegeta report; \
	rm -f "$$account_ids" "$$post_targets" "$$get_targets"

new-migration:
	migrate create -ext sql -dir migrations/ $(name)

migrate-up:
	migrate -path migrations -database "$(DB_DSN)" up

migrate-down:
	migrate -path migrations -database "$(DB_DSN)" down 1

clean:
	rm -rf ./out/
