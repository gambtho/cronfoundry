.PHONY: sqlc schema test test-short build

sqlc:
	cd internal/db && sqlc generate

test:
	go test ./...

test-short:
	go test -short ./...

build:
	go build -o cronfoundry-runner ./cmd/runner
	go build -o cronfoundry       ./cmd/cronfoundry
