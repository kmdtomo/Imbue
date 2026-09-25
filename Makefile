.PHONY: build test generate
build:
	go build -o bin/imbue ./cmd/imbue
	go build -o bin/imbued ./cmd/imbued
test:
	go test -race ./...
generate:
	sqlc generate
