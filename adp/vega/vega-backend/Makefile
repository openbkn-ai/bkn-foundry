.DEFAULT_GOAL := help
.PHONY: help generate-mock lint test test-cover test-race test-at test-at-catalog test-at-verbose test-at-catalog-verbose test-at-setup ci clean-test

COVERAGE_THRESHOLD ?= 0
GOCOVER_COBERTURA := $(shell command -v gocover-cobertura 2>/dev/null)
ifeq ($(GOCOVER_COBERTURA),)
GOCOVER_COBERTURA := $(CURDIR)/test-result/tools/bin/gocover-cobertura
endif

help:
	@echo "vega-backend - 可用命令:"
	@echo "  make test             - 运行单元测试"
	@echo "  make test-cover       - 运行单元测试 + 覆盖率"
	@echo "  make test-race        - 运行单元测试并检查数据竞争"
	@echo "  make lint             - 静态检查"
	@echo "  make ci               - CI 入口 (lint + test-cover)"
	@echo "  make generate-mock    - 重新生成 mock"
	@echo "  make test-at          - 运行所有 AT 测试"
	@echo "  make test-at-catalog  - 运行 Catalog AT 测试"
	@echo "  make test-at-setup    - 检查 AT 测试配置"

generate-mock:
	cd server && go generate ./...

lint:
	cd server && golangci-lint run ./...

define run-unit-tests
	cd server && \
		packages="$$(go list ./...)" && \
		packages="$$(printf '%s\n' "$$packages" | grep -Ev '/(test|tests|mock|mocks)(/|$$)')" && \
		test -n "$$packages" && \
		go test $$packages $(1)
endef

test:
	$(call run-unit-tests,-gcflags=all=-l -v -count=1)

$(GOCOVER_COBERTURA):
	@mkdir -p "$(dir $@)"
	GOBIN="$(dir $@)" go install github.com/boumenot/gocover-cobertura@latest

test-cover: $(GOCOVER_COBERTURA)
	@mkdir -p test-result
	@awk -v threshold="$(COVERAGE_THRESHOLD)" 'BEGIN { \
		if (threshold !~ /^[0-9]+([.][0-9]+)?$$/ || threshold + 0 > 100) exit 1 \
	}' || { \
		echo "错误: COVERAGE_THRESHOLD 必须是 0–100 的数字，当前值为 $(COVERAGE_THRESHOLD)"; \
		exit 1; \
	}
	$(call run-unit-tests,-gcflags=all=-l -coverprofile=../test-result/coverage.raw.out -covermode=atomic -count=1)
	@awk 'NR == 1 { print; next } \
		$$1 !~ /(^|\/)(test|tests|mock|mocks)\// && \
		$$1 !~ /\/mock_[^/]*\.go:/ && \
		$$1 !~ /\/[^/]*_mock\.go:/ && \
		$$1 !~ /\/[^/]*_test\.go:/' \
		test-result/coverage.raw.out > test-result/coverage.out
	@rm -f test-result/coverage.raw.out
	cd server && go tool cover -func=../test-result/coverage.out
	cd server && go tool cover -html=../test-result/coverage.out -o ../test-result/coverage.html
	@coverage="$$(cd server && go tool cover -func=../test-result/coverage.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}')"; \
		test -n "$$coverage" || { echo "错误: 无法读取总覆盖率"; exit 1; }; \
		awk -v coverage="$$coverage" -v threshold="$(COVERAGE_THRESHOLD)" \
			'BEGIN { if (coverage + 0 < threshold + 0) exit 1 }' || { \
			echo "错误: 覆盖率 $$coverage% 低于门禁 $(COVERAGE_THRESHOLD)%"; \
			exit 1; \
		}; \
		echo "覆盖率门禁通过: $$coverage% >= $(COVERAGE_THRESHOLD)%"
	cd server && "$(GOCOVER_COBERTURA)" < ../test-result/coverage.out > ../test-result/coverage.xml
	@grep -Eq 'lines-valid="[1-9][0-9]*"' test-result/coverage.xml || { \
		echo "错误: coverage.xml 未生成有效覆盖率数据"; \
		exit 1; \
	}

test-race:
	$(call run-unit-tests,-gcflags=all=-l -race -count=1)

# === AT Tests (需要运行中的服务 + 外部依赖) ===

test-at: test-at-setup
	@echo "运行 AT 测试..."
	cd tests && go test -v -count=1 ./at/... -timeout 5m

test-at-catalog: test-at-setup
	@echo "运行 Catalog AT 测试..."
	cd tests && go test -v -count=1 ./at/catalog/... -timeout 2m

test-at-verbose: test-at-setup
	@echo "运行 AT 测试（详细输出）..."
	cd tests && go test -v -count=1 ./at/... -timeout 5m

test-at-catalog-verbose: test-at-setup
	@echo "运行 Catalog AT 测试（详细输出）..."
	cd tests && go test -v -count=1 ./at/catalog/... -timeout 2m

test-at-setup:
	@if [ ! -f tests/at/testdata/test-config.yaml ]; then \
		echo "错误: 测试配置文件不存在"; \
		echo "请复制: cp tests/at/testdata/test-config.yaml.example tests/at/testdata/test-config.yaml"; \
		exit 1; \
	fi
	@echo "测试配置文件已就绪"

ci: lint test-cover

clean-test:
	rm -rf test-result
	cd server && rm -f coverage.out
