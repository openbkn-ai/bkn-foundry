.DEFAULT_GOAL := help
.PHONY: help bkn-tools bkn-tools-check

# 能力面产物：随镜像发布，沙箱因此离线可用（见 runtime/sandbox_sdk/bkn.py）。
# 它由 context-loader 的渲染器生成，不是手写的——渲染规则只此一份，线上
# GET /mcp/ptc/toolkit 走的是同一个 BuildPTCToolkit。
BKN_TOOLS = runtime/sandbox_sdk/_bkn_tools.py
CONTEXT_LOADER = ../../adp/context-loader/agent-retrieval

help:
	@echo "可用目标:"
	@echo "  bkn-tools\t\t重新生成沙箱侧 BKN 能力面（改了 MCP 工具 schema 后必须跑）"
	@echo "  bkn-tools-check\t校验产物与当前 schema 一致（CI 用，不写盘）"

bkn-tools:
	@cd $(CONTEXT_LOADER) && go run ./cmd/ptc-stub > $(CURDIR)/$(BKN_TOOLS)
	@echo "已生成 $(BKN_TOOLS)：$$(cd $(CONTEXT_LOADER) && go run ./cmd/ptc-stub --version)"

# 产物签入仓库，就得有人盯着它别过期。漂了只会在调用失败时才暴露，
# 而那时错误信息里看不出根因，所以在 CI 就拦下。
bkn-tools-check:
	@cd $(CONTEXT_LOADER) && go run ./cmd/ptc-stub > /tmp/_bkn_tools.expected.py
	@diff -u $(BKN_TOOLS) /tmp/_bkn_tools.expected.py > /dev/null || { \
		echo "✗ $(BKN_TOOLS) 与当前 MCP 工具 schema 不一致。"; \
		echo "  MCP 工具面改过之后要重新生成：make -C infra/sandbox bkn-tools"; \
		diff -u $(BKN_TOOLS) /tmp/_bkn_tools.expected.py | head -40; \
		exit 1; }
	@echo "✓ $(BKN_TOOLS) 与 schema 一致"
