.DEFAULT_GOAL := help

.PHONY: help lint test test-integration

WORKSPACE_MODULES := server
STANDALONE_MODULES := contract

help:
	@echo "bkn-safe - available commands:"
	@echo "  make lint  - run golangci-lint for every Go module"
	@echo "  make test  - run unit tests for every Go module"
	@echo "  make test-integration - run department locking tests (BKN_SAFE_TEST_MYSQL_DSN required)"

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

test-integration:
	@test -n "$$BKN_SAFE_TEST_MYSQL_DSN" || { echo "BKN_SAFE_TEST_MYSQL_DSN must reference a disposable MySQL/MariaDB test schema"; exit 1; }
	cd server && go test -tags=integration ./internal/directory -run '^TestDepartmentHierarchyMySQL' -count=1 -timeout=30s
