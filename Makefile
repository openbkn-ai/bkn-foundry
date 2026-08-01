# bkn-foundry API 文档工具链
#
# YAML 为唯一真相源，交互式 HTML 由 redocly 渲染、Markdown 由 widdershins 按需本地渲染，
# 产物落 docs/api/_generated/（一律不进 git）。
# 依赖走根 package.json 的 devDependencies（@redocly/cli + widdershins），先 `npm ci` 或 `npm install`。

API_DIR      := docs/api
GEN_DIR      := $(API_DIR)/_generated
HTML_DIR     := $(GEN_DIR)/html
TPL_DIR      := $(API_DIR)/_templates
# 模块目录 = docs/api 下除 _shared / _generated 外的子目录。
# 用 $(API_DIR)/*/. 强制只匹配目录（GNU make 的 */ 通配会把 README.md 也算进来），
# $(dir ...) 取目录路径，再 notdir 取目录名。
MODULE_DIRS  := $(dir $(wildcard $(API_DIR)/*/.))
MODULES      := $(filter-out _shared _generated _templates tools,$(foreach d,$(MODULE_DIRS),$(notdir $(patsubst %/,%,$(d)))))

# 契约巡检:实际返回 vs 文档。默认打开发 VM 的内部面(免 token)。
CONTRACT_SSH        ?= parallels@10.211.55.4
CONTRACT_NS         ?= openbkn
CONTRACT_POD        ?= deploy/bkn-agent
CONTRACT_FACE       ?= in
CONTRACT_ACCOUNT_ID ?= 266c6a42-6131-4d62-8f39-853e7093701c
CONTRACT_OUT        ?= $(GEN_DIR)/contract-report.md
# 额外传给巡检脚本的参数。默认带上 --include-probe-post，含义比 flag 名字重：
# 默认行为从「只发 GET」变成「GET + 所有标了 x-contract-probe.readonly 的 POST」。
# 之所以设成默认，是因为 context-loader 这类服务的查询端点全是 POST，不开就等于
# 零覆盖。安全边界在标注本身：脚本不自行推断哪个 POST 安全，只认 OpenAPI 里显式
# 写的 readonly: true —— 因此**新增该标注等同于授权真打这个接口，评审时按写操作对待**。
# 想退回纯 GET：make api-contract-diff CONTRACT_ARGS=
CONTRACT_ARGS       ?= --include-probe-post

.PHONY: api-docs api-docs-html api-docs-lint api-docs-clean api-contract-diff print-modtitle print-moddesc print-resname

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

## api-contract-diff: 拿运行中的服务真实返回，逐字段比对文档声明的 200 响应 schema。
## lint 只能证明文档「自洽」，这个 target 证明文档「与实现一致」——类型写错、
## 字段拼错、实际多返回的字段，只有真打接口才能发现。
## 只发 GET，以及文档中显式标注 x-contract-probe.readonly 的只读 POST（context-loader
## 这类「查询即 POST」的服务靠它才能覆盖）。退出码非 0 表示存在缺口。变量见文件头 CONTRACT_* 。
api-contract-diff:
	@python3 $(API_DIR)/tools/api_contract_diff.py \
	  --spec-dir $(API_DIR) \
	  --face $(CONTRACT_FACE) \
	  --exec-mode kubectl --ssh $(CONTRACT_SSH) \
	  --namespace $(CONTRACT_NS) --exec-pod $(CONTRACT_POD) \
	  --account-id $(CONTRACT_ACCOUNT_ID) $(CONTRACT_ARGS) \
	  --out $(CONTRACT_OUT) --json-out $(CONTRACT_OUT:.md=.json)

## api-docs: 渲染各模块 YAML 为 Markdown，输出到 _generated/<module>.md。
## 本地按需生成（喂飞书 / 离线阅读等），产物不进 git、CI 不渲染。
## 每个 YAML 先渲染到临时文件（widdershins 的编译日志走 stdout，不能用 -o -），
## 再按模块拼接。--code 关掉多语言代码示例（PHP/Ruby/… 对 REST 参照是噪声）。
api-docs:
	@mkdir -p $(GEN_DIR)
	@rm -f $(GEN_DIR)/*.md
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

## 模块显示标题（index 分区标题用）。未列出的模块回落为目录名。
MODTITLE_bkn               := BKN
MODTITLE_bkn-agent         := BKN 专属 Agent
MODTITLE_execution-factory := 执行工厂
MODTITLE_agent-observability := BKN Trace
MODTITLE_context-loader    := 上下文加载
MODTITLE_mf-model-manager  := 模型管理
MODTITLE_ontology-query    := Ontology 查询
MODTITLE_vega              := VEGA 引擎

## 模块中文描述（index 卡片副标题用）。未列出的模块回落为空。
MODDESC_bkn               := 业务知识网络：对象类 / 关系类 / 行动类 / 概念组 / 指标 / 导入导出
MODDESC_bkn-agent         := Agent 运行时：Agent 增删改查 / 对话与调用 / 任务 / 提示词版本 / 会话 / 导入导出
MODDESC_execution-factory := 能力落地：函数编写与沙箱执行 / 依赖与模板 / AI 生成（算子、工具箱、MCP、Skill 待补）
MODDESC_agent-observability := BKN Trace：受管会话生命周期 / 业务证据 / 技术链路 / 快照
MODDESC_context-loader    := Agent 上下文入口：Schema 检索 / 实例与子图查询 / 逻辑属性 / 行动执行 / Skill 召回 / 数据直查 / MCP
MODDESC_mf-model-manager  := 模型工厂：大模型连通性测试 / 默认模型设置 / 调用监控
MODDESC_ontology-query    := 本体查询与语义检索
MODDESC_vega              := 数据可观测：目录 / 资源 / 连接器 / 构建任务 / 发现任务 / 原生查询

# 资源中文名（侧栏显示用；未列出的回落为文件名）
RESNAME_bkn-agent                 := Agent 运行时
RESNAME_agent-observability       := BKN Trace
RESNAME_large-model               := 大模型
RESNAME_semantic-understanding-task := 语义理解任务
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
RESNAME_schema-search             := Schema 检索
RESNAME_kn-explore                := 知识网络浏览
RESNAME_object-instance           := 对象实例查询
RESNAME_instance-subgraph         := 实例子图查询
RESNAME_logic-property            := 逻辑属性求值
RESNAME_action                    := 行动召回与执行
RESNAME_data-access               := 数据层直查
# context-loader（模块限定，避免与 execution-factory 的同名文件互相覆盖）
RESNAME_context-loader_mcp        := MCP 服务
RESNAME_context-loader_skill      := Skill 召回
# execution-factory
RESNAME_function                  := 函数
RESNAME_sandbox                   := 沙箱观测
RESNAME_impex                     := 导入导出
RESNAME_operator                  := 算子
RESNAME_toolbox                   := 工具箱
RESNAME_execution-factory_mcp     := MCP
RESNAME_execution-factory_skill   := Skill

## api-docs-html: 用 redocly 为每个 YAML 渲染交互式 HTML 文档（带搜索/折叠/示例），
## 输出到 _generated/html/<module>/<resource>.html，并生成一个卡片式 index.html 汇总入口。
## index 的静态头/尾模板在 $(TPL_DIR)/index-{head,foot}.html，中间的模块卡片按数据生成。
## HTML 不进 git（见 .gitignore），由 CI 渲染并发布到 GitHub Pages；本地也可自行生成查看。
## 渲染分两步：先把全部 YAML 用 redocly 并行转 HTML（重活），再串行拼 index（快）。
## REDOCLY_TELEMETRY=off 掐掉 redocly 的联网遥测/更新检查（单文件 4.6s -> 1s）；
## redocly 走本地 devDependency，先 `npm install`，否则 npx 每次上网解析（单文件 30s）。
api-docs-html: export REDOCLY_TELEMETRY=off
api-docs-html:
	@rm -rf $(HTML_DIR)
	@mkdir -p $(HTML_DIR) $(foreach m,$(MODULES),$(HTML_DIR)/$(m))
	@cp "$(TPL_DIR)/openbkn-logo.png" "$(HTML_DIR)/openbkn-logo.png"
	@cp "$(TPL_DIR)/favicon.png" "$(HTML_DIR)/favicon.png"
	@cp "$(TPL_DIR)/auth.html" "$(HTML_DIR)/auth.html"
	@echo "==> html: rendering $(words $(foreach m,$(MODULES),$(wildcard $(API_DIR)/$(m)/*.yaml))) yaml (parallel)"
	@printf '%s\n' $(foreach m,$(MODULES),$(wildcard $(API_DIR)/$(m)/*.yaml)) | \
	  xargs -n 1 -P 8 sh -c 'y="$$1"; m=$$(basename "$$(dirname "$$y")"); b=$$(basename "$$y" .yaml); \
	    npx @redocly/cli build-docs "$$y" -o "$(HTML_DIR)/$$m/$$b.html" >/dev/null 2>&1 \
	    || { echo "build-docs failed: $$y" >&2; exit 1; }' _ \
	  || { echo "api-docs-html: 渲染失败（见上方 failed 行）" >&2; exit 1; }
	@idx="$(HTML_DIR)/index.html"; \
	cat "$(TPL_DIR)/index-head.html" > "$$idx"; \
	for m in $(MODULES); do \
	  mt=$$(make -s print-modtitle MOD="$$m"); [ -n "$$mt" ] || mt="$$m"; \
	  desc=$$(make -s print-moddesc MOD="$$m"); \
	  count=$$(ls $(API_DIR)/$$m/*.yaml 2>/dev/null | wc -l | tr -d ' '); \
	  printf '<section class="mod">\n<div class="mod-h"><h2>%s</h2><span class="count">%s</span></div>\n' "$$mt" "$$count" >> "$$idx"; \
	  [ -n "$$desc" ] && printf '<p class="mod-desc">%s</p>\n' "$$desc" >> "$$idx"; \
	  printf '<div class="grid">\n' >> "$$idx"; \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    base=$$(basename "$$y" .yaml); \
	    rn=$$(make -s print-resname MOD="$$m" RES="$$base"); [ -n "$$rn" ] || rn="$$base"; \
	    printf '<a class="card" data-name="%s %s" href="./%s/%s.html" target="_blank" rel="noopener"><span class="name">%s</span><span class="arrow">&rarr;</span></a>\n' "$$base" "$$rn" "$$m" "$$base" "$$rn" >> "$$idx"; \
	  done; \
	  printf '</div>\n</section>\n' >> "$$idx"; \
	done; \
	cat "$(TPL_DIR)/index-foot.html" >> "$$idx"
	@echo "done -> $(HTML_DIR)/ (open index.html)"

## print-modtitle: 内部辅助，回显某模块的显示标题（供 index 生成用）
print-modtitle:
	@echo "$(MODTITLE_$(MOD))"

## print-moddesc: 内部辅助，回显某模块的中文描述（供 index 生成用）
print-moddesc:
	@echo "$(MODDESC_$(MOD))"

## print-resname: 内部辅助，回显某资源的中文名（供侧栏显示用）。
## 先查模块限定的 RESNAME_<模块>_<资源>，再回落到全局的 RESNAME_<资源>。
## 需要限定是因为不同模块会有同名文件：context-loader 与 execution-factory
## 都有 mcp.yaml / skill.yaml，扁平命名空间下后者会覆盖前者的显示名。
print-resname:
	@if [ -n "$(RESNAME_$(MOD)_$(RES))" ]; then echo "$(RESNAME_$(MOD)_$(RES))"; \
	else echo "$(RESNAME_$(RES))"; fi

## api-docs-clean: 清空 _generated 的产物（渲染前重建，避免删源后残留旧文件）
api-docs-clean:
	@rm -rf $(GEN_DIR)
