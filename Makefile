.PHONY: build run test

BIN_DIR := bin
BIN := $(BIN_DIR)/git-mj-rebase

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN) ./cmd/git-mj-rebase

run:
	go run ./cmd/git-mj-rebase --help

test:
	go test ./...
