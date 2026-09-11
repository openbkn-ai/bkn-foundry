.DEFAULT_GOAL := help

.PHONY: help lint test

WORKSPACE_MODULES := server
STANDALONE_MODULES := contract cmd/authz-shadow

help:
	@echo "bkn-safe - available commands:"
	@echo "  make lint  - run golangci-lint for every Go module"
	@echo "  make test  - run unit tests for every Go module"

lint:
	@for module in $(WORKSPACE_MODULES); do \
		echo "==> lint $$module"; \
		(cd $$module && golangci-lint run ./...) || exit $$?; \
	done
	@for module in $(STANDALONE_MODULES); do \
		echo "==> lint $$module"; \
		(cd $$module && GOWORK=off golangci-lint run ./...) || exit $$?; \
	done

test:
	@for module in $(WORKSPACE_MODULES); do \
		echo "==> test $$module"; \
		(cd $$module && go test ./...) || exit $$?; \
	done
	@for module in $(STANDALONE_MODULES); do \
		echo "==> test $$module"; \
		(cd $$module && GOWORK=off go test ./...) || exit $$?; \
	done
