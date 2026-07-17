# bkn-foundry API 文档工具链
#
# YAML 为唯一真相源，Markdown 由 widdershins 从 YAML 渲染，产物落 docs/api/_generated/。
# 依赖走根 package.json 的 devDependencies（widdershins + @redocly/cli），先 `npm ci` 或 `npm install`。

API_DIR      := docs/api
GEN_DIR      := $(API_DIR)/_generated
HTML_DIR     := $(GEN_DIR)/html
TPL_DIR      := $(API_DIR)/_templates
# 模块目录 = docs/api 下除 _shared / _generated 外的子目录。
# 用 $(API_DIR)/*/. 强制只匹配目录（GNU make 的 */ 通配会把 README.md 也算进来），
# $(dir ...) 取目录路径，再 notdir 取目录名。
MODULE_DIRS  := $(dir $(wildcard $(API_DIR)/*/.))
MODULES      := $(filter-out _shared _generated _templates,$(foreach d,$(MODULE_DIRS),$(notdir $(patsubst %/,%,$(d)))))

.PHONY: api-docs api-docs-html api-docs-lint api-docs-clean print-moddesc print-resname

## api-docs-lint: 校验各模块 OpenAPI 文档合法且 $ref（含共享 schema）可解析。
## _shared/ 是 $ref 片段（无 openapi/info/paths 顶层），不作独立文档 lint，
## 其正确性由引用它的模块文档解析时连带校验。
api-docs-lint:
	@set -e; for m in $(MODULES); do \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    npx @redocly/cli lint --config .redocly.yaml "$$y"; \
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

## 模块中文描述（index 卡片副标题用）。未列出的模块回落为空。
MODDESC_bkn            := 业务知识网络：对象类 / 关系类 / 行动类 / 概念组 / 指标 / 导入导出
MODDESC_ontology-query := 本体查询与语义检索
MODDESC_vega           := 数据可观测：目录 / 资源 / 连接器 / 构建任务 / 发现任务 / 原生查询

# 资源中文名（侧栏显示用；未列出的回落为文件名）
RESNAME_action-schedules          := 行动调度
RESNAME_action-type               := 行动类
RESNAME_bkn-metrics               := 指标
RESNAME_bkn                       := 导入导出
RESNAME_business-knowledge-network := 知识网络
RESNAME_concept-group             := 概念组
RESNAME_job                       := 任务
RESNAME_object-type               := 对象类
RESNAME_relation-type             := 关系类
RESNAME_risk-types                := 风险类
RESNAME_auth-resource             := 资源授权
RESNAME_build-task                := 构建任务
RESNAME_catalog                   := 目录
RESNAME_connector-type            := 连接器类型
RESNAME_discover-schedule         := 发现调度
RESNAME_discover-task             := 发现任务
RESNAME_raw-query                 := 原生查询
RESNAME_resource-data             := 资源数据
RESNAME_resource                  := 资源
RESNAME_ontology-query            := 本体查询

## api-docs-html: 用 redocly 为每个 YAML 渲染交互式 HTML 文档（带搜索/折叠/示例），
## 输出到 _generated/html/<module>/<resource>.html，并生成一个卡片式 index.html 汇总入口。
## index 的静态头/尾模板在 $(TPL_DIR)/index-{head,foot}.html，中间的模块卡片按数据生成。
## HTML 不进 git（见 .gitignore），由 CI 渲染并发布到 GitHub Pages；本地也可自行生成查看。
api-docs-html:
	@rm -rf $(HTML_DIR)
	@mkdir -p $(HTML_DIR)
	@cp "$(TPL_DIR)/openbkn-logo.png" "$(HTML_DIR)/openbkn-logo.png"
	@cp "$(TPL_DIR)/auth.html" "$(HTML_DIR)/auth.html"
	@idx="$(HTML_DIR)/index.html"; \
	cat "$(TPL_DIR)/index-head.html" > "$$idx"; \
	for m in $(MODULES); do \
	  echo "==> html: $$m"; \
	  mkdir -p "$(HTML_DIR)/$$m"; \
	  desc=$$(make -s print-moddesc MOD="$$m"); \
	  count=$$(ls $(API_DIR)/$$m/*.yaml 2>/dev/null | wc -l | tr -d ' '); \
	  printf '<section class="mod">\n<div class="mod-h"><h2>%s</h2><span class="count">%s</span></div>\n' "$$m" "$$count" >> "$$idx"; \
	  [ -n "$$desc" ] && printf '<p class="mod-desc">%s</p>\n' "$$desc" >> "$$idx"; \
	  printf '<div class="grid">\n' >> "$$idx"; \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    base=$$(basename "$$y" .yaml); \
	    rn=$$(make -s print-resname RES="$$base"); [ -n "$$rn" ] || rn="$$base"; \
	    npx @redocly/cli build-docs "$$y" -o "$(HTML_DIR)/$$m/$$base.html" >/dev/null 2>&1 || { echo "build-docs failed: $$y"; exit 1; }; \
	    printf '<a class="card" data-name="%s %s" href="./%s/%s.html" target="_blank" rel="noopener"><span class="name">%s</span><span class="arrow">&rarr;</span></a>\n' "$$base" "$$rn" "$$m" "$$base" "$$rn" >> "$$idx"; \
	  done; \
	  printf '</div>\n</section>\n' >> "$$idx"; \
	done; \
	cat "$(TPL_DIR)/index-foot.html" >> "$$idx"
	@echo "done -> $(HTML_DIR)/ (open index.html)"

## print-moddesc: 内部辅助，回显某模块的中文描述（供 index 生成用）
print-moddesc:
	@echo "$(MODDESC_$(MOD))"

## print-resname: 内部辅助，回显某资源的中文名（供侧栏显示用）
print-resname:
	@echo "$(RESNAME_$(RES))"

## api-docs-clean: 清空 _generated 的产物（渲染前重建，避免删源后残留旧文件）
api-docs-clean:
	@rm -f $(GEN_DIR)/*.md
	@rm -rf $(HTML_DIR)
