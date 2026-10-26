.DEFAULT_GOAL := help
.PHONY: help generate-mock lint test test-cover test-race ci clean-test

help:
	@echo "bkn-project - available targets:"
	@echo "  make test             - run unit tests"
	@echo "  make test-cover       - run unit tests with coverage"
	@echo "  make test-race        - run unit tests with race detection"
	@echo "  make lint             - static analysis"
	@echo "  make ci               - CI entry (lint + test-cover)"
	@echo "  make generate-mock    - regenerate mock files"

generate-mock:
	cd server && go generate ./...

lint:
	cd server && golangci-lint run ./...

UT_PACKAGES = $(shell cd server && go list ./... | grep -v /tests/ | grep -v /mock)

test:
	cd server && I18N_MODE_UT=true go test $(UT_PACKAGES) -gcflags=all=-l -v -count=1

test-cover:
	@mkdir -p test-result
	cd server && I18N_MODE_UT=true go test $(UT_PACKAGES) -gcflags=all=-l \
		-coverprofile=../test-result/coverage.out \
		-covermode=atomic
	go tool cover -func=test-result/coverage.out
	go tool cover -html=test-result/coverage.out -o test-result/coverage.html

test-race:
	cd server && I18N_MODE_UT=true go test $(UT_PACKAGES) -gcflags=all=-l -race -count=1

ci: lint test-cover

clean-test:
	rm -rf test-result
	cd server && rm -f coverage.out
