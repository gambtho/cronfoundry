.PHONY: sqlc test test-short build web vet lint dev dev-down migrate e2e clean help worktree-clean

help:
	@echo 'Targets:'
	@echo '  build        Build cronfoundry + cronfoundry-runner binaries (runs web first)'
	@echo '  web          Build the React UI bundle into internal/webapi/web/dist'
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

build: web
	go build -o cronfoundry-runner ./cmd/runner
	go build -o cronfoundry       ./cmd/cronfoundry

test:
	go test ./... -count=1 -timeout 10m

test-short:
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

e2e:
	go test -tags=e2e ./cmd/cronfoundry/... -count=1 -timeout 10m -run TestE2E_

clean:
	rm -f cronfoundry cronfoundry-runner

worktree-clean:
	@echo 'Pruning stale worktree admin records...'
	git worktree prune -v
	@echo ''
	@echo 'Remaining worktrees (review and remove unwanted ones manually with `git worktree remove <path>`):'
	@git worktree list
