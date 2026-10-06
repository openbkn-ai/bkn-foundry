.PHONY: test lint build ci

test:
	go test ./... -count=1

lint:
	go vet ./...

build:
	go build -o /tmp/bkn-historical-span-codec .

ci: lint test build
