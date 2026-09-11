.DEFAULT_GOAL := help

.PHONY: help lint test

MODULES := server contract cmd/authz-shadow

help:
	@echo "bkn-safe - available commands:"
	@echo "  make lint  - run golangci-lint for every Go module"
	@echo "  make test  - run unit tests for every Go module"

lint:
	@for module in $(MODULES); do \
		echo "==> lint $$module"; \
		(cd $$module && golangci-lint run ./...) || exit $$?; \
	done

test:
	@for module in $(MODULES); do \
		echo "==> test $$module"; \
		(cd $$module && go test ./...) || exit $$?; \
	done
