# bkn-foundry API 文档工具链
#
# YAML 为唯一真相源，Markdown 由 widdershins 从 YAML 渲染，产物落 docs/api/_generated/。
# 依赖走根 package.json 的 devDependencies（widdershins + @redocly/cli），先 `npm ci` 或 `npm install`。

API_DIR      := docs/api
GEN_DIR      := $(API_DIR)/_generated
HTML_DIR     := $(GEN_DIR)/html
# 模块目录 = docs/api 下除 _shared / _generated 外的子目录。
# 用 $(API_DIR)/*/. 强制只匹配目录（GNU make 的 */ 通配会把 README.md 也算进来），
# $(dir ...) 取目录路径，再 notdir 取目录名。
MODULE_DIRS  := $(dir $(wildcard $(API_DIR)/*/.))
MODULES      := $(filter-out _shared _generated,$(foreach d,$(MODULE_DIRS),$(notdir $(patsubst %/,%,$(d)))))

.PHONY: api-docs api-docs-html api-docs-lint api-docs-clean print-moddesc

## api-docs-lint: 校验各模块 OpenAPI 文档合法且 $ref（含共享 schema）可解析。
## _shared/ 是 $ref 片段（无 openapi/info/paths 顶层），不作独立文档 lint，
## 其正确性由引用它的模块文档解析时连带校验。
api-docs-lint:
	@set -e; for m in $(MODULES); do \
	  for y in $(API_DIR)/$$m/*.yaml; do \
	    [ -e "$$y" ] || continue; \
	    npx @redocly/cli lint "$$y"; \
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
MODDESC_vega           := 数据可观测：Catalog / 资源 / 连接器 / 构建任务 / 发现任务 / 原生查询
MODDESC_dataflow       := 文档流处理管线

# index.html 的静态头部（含样式，主题自适应）。`$$` 是写进文件的字面 `$`。
define INDEX_HEAD
<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>BKN Foundry API 文档</title>
<style>
  :root{--bg:#f7f9fb;--fg:#12212b;--muted:#5b6b78;--card:#fff;--line:#e4eaf0;--acc:#0e7c86;--acc-bg:#e1f3f4;--shadow:0 1px 2px rgba(16,32,44,.04),0 3px 12px rgba(16,32,44,.06)}
  @media(prefers-color-scheme:dark){:root{--bg:#0d1117;--fg:#e6edf3;--muted:#8b98a5;--card:#161b22;--line:#232b34;--acc:#3fb8c2;--acc-bg:#0e3037;--shadow:0 1px 2px rgba(0,0,0,.3),0 3px 12px rgba(0,0,0,.4)}}
  *{box-sizing:border-box}
  body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 -apple-system,BlinkMacSystemFont,"Segoe UI","PingFang SC","Microsoft YaHei",system-ui,sans-serif;-webkit-font-smoothing:antialiased}
  .wrap{max-width:960px;margin:0 auto;padding:56px 24px 96px}
  header h1{font-size:30px;letter-spacing:-.02em;margin:0 0 8px}
  header p{color:var(--muted);font-size:16px;margin:0 0 28px}
  .search{width:100%;max-width:420px;padding:10px 14px;font-size:15px;border:1px solid var(--line);border-radius:10px;background:var(--card);color:var(--fg);margin-bottom:36px;outline:none}
  .search:focus{border-color:var(--acc)}
  .mod{margin:0 0 36px}
  .mod-h{display:flex;align-items:baseline;gap:12px;margin:0 0 4px}
  .mod-h h2{font-size:20px;margin:0}
  .mod-h .count{font:600 12px system-ui;color:var(--acc);background:var(--acc-bg);padding:2px 9px;border-radius:20px}
  .mod-desc{color:var(--muted);font-size:14px;margin:0 0 16px}
  .grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(220px,1fr));gap:12px}
  .card{display:block;padding:14px 16px;background:var(--card);border:1px solid var(--line);border-radius:12px;box-shadow:var(--shadow);text-decoration:none;color:inherit;transition:transform .12s,border-color .12s}
  .card:hover{transform:translateY(-2px);border-color:var(--acc)}
  .card .name{font-weight:600;font-size:15px}
  .card .arrow{color:var(--acc);float:right}
  .empty{color:var(--muted);font-size:14px}
  footer{margin-top:48px;color:var(--muted);font-size:13px;border-top:1px solid var(--line);padding-top:20px}
</style>
</head>
<body>
<div class="wrap">
<header>
  <h1>BKN Foundry API 文档</h1>
  <p>各服务的 OpenAPI 交互式文档 —— 点开任意资源查看端点、参数与示例。</p>
  <input class="search" id="q" type="search" placeholder="过滤资源…" autocomplete="off">
</header>
<main id="list">
endef

define INDEX_FOOT
</main>
<footer>YAML 为唯一真相源；本页由 <code>make api-docs-html</code> 从 OpenAPI 渲染。</footer>
</div>
<script>
  var q=document.getElementById('q'),cards=[].slice.call(document.querySelectorAll('.card')),mods=[].slice.call(document.querySelectorAll('.mod'));
  q.addEventListener('input',function(){
    var v=q.value.trim().toLowerCase();
    cards.forEach(function(c){c.style.display=c.dataset.name.indexOf(v)>-1?'':'none';});
    mods.forEach(function(m){var any=[].slice.call(m.querySelectorAll('.card')).some(function(c){return c.style.display!=='none';});m.style.display=any?'':'none';});
  });
</script>
</body>
</html>
endef

export INDEX_HEAD
export INDEX_FOOT

## api-docs-html: 用 redocly 为每个 YAML 渲染交互式 HTML 文档（带搜索/折叠/示例），
## 输出到 _generated/html/<module>/<resource>.html，并生成一个卡片式 index.html 汇总入口。
## HTML 不进 git（见 .gitignore），由 CI 渲染并发布到 GitHub Pages；本地也可自行生成查看。
api-docs-html:
	@rm -rf $(HTML_DIR)
	@mkdir -p $(HTML_DIR)
	@idx="$(HTML_DIR)/index.html"; \
	printf '%s\n' "$$INDEX_HEAD" > "$$idx"; \
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
	    npx @redocly/cli build-docs "$$y" -o "$(HTML_DIR)/$$m/$$base.html" >/dev/null 2>&1 || { echo "build-docs failed: $$y"; exit 1; }; \
	    printf '<a class="card" data-name="%s" href="./%s/%s.html"><span class="arrow">&rarr;</span><span class="name">%s</span></a>\n' "$$base" "$$m" "$$base" "$$base" >> "$$idx"; \
	  done; \
	  printf '</div>\n</section>\n' >> "$$idx"; \
	done; \
	printf '%s\n' "$$INDEX_FOOT" >> "$$idx"
	@echo "done -> $(HTML_DIR)/ (open index.html)"

## print-moddesc: 内部辅助，回显某模块的中文描述（供 index 生成用）
print-moddesc:
	@echo "$(MODDESC_$(MOD))"

## api-docs-clean: 清空 _generated 的产物（渲染前重建，避免删源后残留旧文件）
api-docs-clean:
	@rm -f $(GEN_DIR)/*.md
	@rm -rf $(HTML_DIR)
