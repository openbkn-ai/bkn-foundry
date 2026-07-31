.DEFAULT_GOAL := gen-swag

SWAG_VERSION := v1.8.12
SWAG ?= go run github.com/swaggo/swag/cmd/swag@v1.8.12
SWAG_OUT := docs/swagger
IMAGE ?= swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability:local
CHART_PATH := charts/agent-observability
GOCACHE ?= /tmp/go-build
TEST_RESULT := test-result
UNIT_PACKAGES := ./...
RACE_PACKAGES := ./src/domain/service/... ./src/drivenadapter/memoryaccess/...
INTEGRATION_PACKAGE := ./src/drivenadapter/dbaccess/mariadb/sessionstore
OPENSEARCH_INTEGRATION_PACKAGE := ./src/drivenadapter/httpaccess/opensearchprojection

.PHONY: lint test test-cover test-race test-integration test-opensearch-integration test-performance ci build \
	docker-build helm-lint helm-package check-swag-version gen-swag check-swag view-swag clean-swag

lint:
	GOCACHE=$(GOCACHE) go vet ./...

test:
	GOCACHE=$(GOCACHE) go test $(UNIT_PACKAGES) -count=1

test-cover:
	@mkdir -p $(TEST_RESULT)
	GOCACHE=$(GOCACHE) go test $(UNIT_PACKAGES) -count=1 -covermode=atomic \
		-coverprofile=$(TEST_RESULT)/coverage.out
	go tool cover -func=$(TEST_RESULT)/coverage.out
	go tool cover -html=$(TEST_RESULT)/coverage.out -o $(TEST_RESULT)/coverage.html
	@command -v gocover-cobertura >/dev/null 2>&1 && \
		gocover-cobertura < $(TEST_RESULT)/coverage.out > $(TEST_RESULT)/coverage.xml || \
		echo "WARN: gocover-cobertura not found; coverage.xml was not generated"

test-race:
	GOCACHE=$(GOCACHE) go test -race $(RACE_PACKAGES) -count=1

test-integration:
	GOCACHE=$(GOCACHE) go test -tags=integration $(INTEGRATION_PACKAGE) -count=1 -v

test-opensearch-integration:
	GOCACHE=$(GOCACHE) go test -tags=integration $(OPENSEARCH_INTEGRATION_PACKAGE) -count=1 -v

test-performance:
	GOCACHE=$(GOCACHE) go test -tags=integration $(INTEGRATION_PACKAGE) -run '^$$' \
		-bench 'BenchmarkMariaDB(EnsureCurrent|EvidenceIngest)' -benchtime=200x -count=1

ci: lint test-cover

build:
	GOCACHE=$(GOCACHE) go build ./...

docker-build:
	docker build -t $(IMAGE) .

helm-lint:
	helm lint $(CHART_PATH)
	bash scripts/test_evidence_index_governance.sh $(CHART_PATH)

helm-package:
	mkdir -p dist
	helm package $(CHART_PATH) --destination dist

check-swag-version:
	@$(SWAG) --version | grep -q "version $(SWAG_VERSION)$$" || \
		(echo "Swagger CLI must be $(SWAG_VERSION)" >&2; exit 1)

gen-swag: check-swag-version
	$(SWAG) init -g main.go -o $(SWAG_OUT) --parseDependency --parseInternal

check-swag: check-swag-version
	@set -e; \
		tmp=$$(mktemp -d); \
		trap 'rm -rf "$$tmp"' EXIT; \
		$(SWAG) init -g main.go -o "$$tmp" --packageName swagger --parseDependency --parseInternal; \
		for file in docs.go swagger.json swagger.yaml; do \
			diff -u $(SWAG_OUT)/$$file $$tmp/$$file; \
		done

view-swag:
	@echo "Swagger JSON: http://localhost:8080/swagger/swagger.json"
	@echo "Swagger YAML: http://localhost:8080/swagger/swagger.yaml"

clean-swag:
	rm -rf $(SWAG_OUT)
	$(MAKE) gen-swag
