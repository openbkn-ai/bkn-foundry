.DEFAULT_GOAL := help

.PHONY: help lint test

help:
	@echo "comm-go - available commands:"
	@echo "  make lint  - run golangci-lint"
	@echo "  make test  - run unit tests"

lint:
	golangci-lint run ./...

test:
	go test ./...
