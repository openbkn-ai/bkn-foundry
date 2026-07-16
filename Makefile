# bkn-foundry API 文档工具链
#
# YAML 为唯一真相源，Markdown 由 widdershins 从 YAML 渲染，产物落 docs/api/_generated/。
# 依赖走根 package.json 的 devDependencies（widdershins + @redocly/cli），先 `npm ci` 或 `npm install`。

API_DIR      := docs/api
GEN_DIR      := $(API_DIR)/_generated
# 模块目录 = docs/api 下除 _shared / _generated 外的子目录。
# 用 $(API_DIR)/*/. 强制只匹配目录（GNU make 的 */ 通配会把 README.md 也算进来），
# $(dir ...) 取目录路径，再 notdir 取目录名。
MODULE_DIRS  := $(dir $(wildcard $(API_DIR)/*/.))
MODULES      := $(filter-out _shared _generated,$(foreach d,$(MODULE_DIRS),$(notdir $(patsubst %/,%,$(d)))))

.PHONY: api-docs api-docs-lint api-docs-clean

## api-docs-lint: 校验各模块 OpenAPI 文档合法且 $ref（含共享 schema）可解析。
## _shared/ 是 $ref 片段（无 openapi/info/paths 顶层），不作独立文档 lint，
## 其正确性由引用它的模块文档解析时连带校验。
api-docs-lint:
	@set -e; for m in $(MODULES); do \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    npx redocly lint "$$y"; \
	  done; \
	done

## api-docs: 渲染各模块 YAML 为 Markdown，输出到 _generated/<module>.md
## 每个 YAML 先渲染到临时文件（widdershins 的编译日志走 stdout，不能用 -o -），
## 再按模块拼接。--code 关掉多语言代码示例（PHP/Ruby/… 对 REST 参照是噪声）。
api-docs: api-docs-clean
	@mkdir -p $(GEN_DIR)
	@tmp=$$(mktemp); \
	for m in $(MODULES); do \
	  echo "==> rendering $$m"; \
	  : > "$(GEN_DIR)/$$m.md"; \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    npx widdershins --code --summary --omitHeader "$$y" -o "$$tmp" >/dev/null 2>&1 || { echo "render failed: $$y"; rm -f "$$tmp"; exit 1; }; \
	    cat "$$tmp" >> "$(GEN_DIR)/$$m.md"; \
	    printf '\n\n' >> "$(GEN_DIR)/$$m.md"; \
	  done; \
	  perl -i -ne 'print unless /^> Scroll down for code samples/' "$(GEN_DIR)/$$m.md"; \
	done; \
	rm -f "$$tmp"
	@echo "done -> $(GEN_DIR)/"

## api-docs-clean: 清空 _generated（渲染前重建，避免删源后残留旧 md）
api-docs-clean:
	@rm -f $(GEN_DIR)/*.md
