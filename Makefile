.PHONY: sqlc test test-short build web web-stub vet lint dev dev-down migrate e2e clean help worktree-clean

help:
	@echo 'Targets:'
	@echo '  build        Build cronfoundry + cronfoundry-runner binaries (runs web first)'
	@echo '  web          Build the real React UI bundle into internal/webapi/web/dist'
	@echo '  web-stub     Write a placeholder index.html into internal/webapi/web/dist'
	@echo '               (satisfies the go:embed directive when the real UI is not needed)'
	@echo '  test         Run all tests (with docker/testcontainers integration)'
	@echo '  test-short   Run unit tests only (no containers)'
	@echo '  vet          go vet ./...'
	@echo '  lint         go vet + gofmt check'
	@echo '  sqlc         Regenerate internal/db/gen/*.go from queries/'
	@echo '  dev          Start docker-compose stack (Postgres + cronfoundry serve)'
	@echo '  dev-down     Stop + remove docker-compose stack'
	@echo '  migrate      Run goose migrations against $$CRONFOUNDRY_DATABASE_URL'
	@echo '  e2e          Run the end-to-end integration test (requires docker)'
	@echo '  clean        Remove built binaries'
	@echo '  worktree-clean  Prune stale worktree admin records and list remaining worktrees'

web:
	cd web && npm ci && npm run build

# web-stub satisfies the //go:embed all:web/dist directive without running
# Vite. Used by CI jobs that compile/test Go but don't exercise the UI, and
# by local dev when iterating on backend code only.
WEB_DIST_DIR := internal/webapi/web/dist
web-stub:
	@mkdir -p $(WEB_DIST_DIR)
	@if [ ! -f $(WEB_DIST_DIR)/index.html ]; then \
	  printf '<!doctype html>\n<html><head><meta charset="utf-8"><title>CronFoundry (stub)</title></head><body>Stub UI — run `make web` for the real bundle.</body></html>\n' > $(WEB_DIST_DIR)/index.html; \
	  echo 'wrote stub $(WEB_DIST_DIR)/index.html'; \
	else \
	  echo '$(WEB_DIST_DIR)/index.html already exists; not overwriting'; \
	fi

build: web
	go build -o cronfoundry-runner ./cmd/runner
	go build -o cronfoundry       ./cmd/cronfoundry

test: web-stub
	go test ./... -count=1 -timeout 10m

test-short: web-stub
	go test -short ./...

vet:
	go vet ./...

lint: vet
	@unformatted=$$(gofmt -l .); \
	 if [ -n "$$unformatted" ]; then echo "Unformatted files:"; echo "$$unformatted"; exit 1; fi

sqlc:
	cd internal/db && sqlc generate

dev:
	cd deploy && docker compose up -d --build
	@echo 'Stack up. Tail logs with: cd deploy && docker compose logs -f cronfoundry'

dev-down:
	cd deploy && docker compose down -v

migrate:
	@if [ -z "$$CRONFOUNDRY_DATABASE_URL" ]; then \
	  echo 'CRONFOUNDRY_DATABASE_URL not set'; exit 1; \
	 fi
	@if [ -z "$$CRONFOUNDRY_MASTER_KEY" ]; then \
	  echo 'CRONFOUNDRY_MASTER_KEY not set (run `cronfoundry admin init` to generate one)'; exit 1; \
	 fi
	go run ./cmd/cronfoundry admin init

e2e: web-stub
	go test -tags=e2e ./cmd/cronfoundry/... -count=1 -timeout 10m -run TestE2E_

clean:
	rm -f cronfoundry cronfoundry-runner
	rm -rf $(WEB_DIST_DIR)

worktree-clean:
	@echo 'Pruning stale worktree admin records...'
	git worktree prune -v
	@echo ''
	@echo 'Remaining worktrees (review and remove unwanted ones manually with `git worktree remove <path>`):'
	@git worktree list
