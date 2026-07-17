# 📚 API 文档

本目录统一收纳 bkn-foundry 各服务的 **OpenAPI 文档**。YAML 是唯一真相源，Markdown 由工具从 YAML 自动渲染。

## 👀 如何查看

- **在线（推荐）**：合并到 `main` 后由 CI 发布到 **GitHub Pages**，带版本下拉、按模块的交互式文档（搜索 / 折叠 / 示例）与认证说明，一个链接看全部。
- **Markdown 直读**：[`_generated/`](_generated/) 下每个模块一份 md，可在 GitHub / 飞书直接阅读（见下表）。
- **本地生成交互式 HTML**：

  ```bash
  npm install          # 首次：装 widdershins + @redocly/cli
  make api-docs-html   # 渲染到 _generated/html/，打开 index.html 查看
  ```

## 🔑 如何调用（认证）

接口需认证，请求头带 `Authorization: Bearer <token>`。获取 token：**① CLI 登录**（`openbkn auth login`，token 存 `~/.bkn/` 自动携带）；**② AppKey**（`POST /api/safe/v1/me/api-keys` 签发 `bak_` 密钥，适合自动化）；**③ OAuth2**（Hydra `POST /oauth2/token`）。完整示例见在线文档首页的「认证」区块。

## 🗂️ 模块一览

| 模块 | 目录 | 说明 | Markdown |
|---|---|---|---|
| 🟦 bkn-backend | [`bkn/`](bkn/) | 业务知识网络：对象类 / 关系类 / 行动类 / 概念组 / 指标 / 导入导出 | [`_generated/bkn.md`](_generated/bkn.md) |
| 🟩 ontology-query | [`ontology-query/`](ontology-query/) | 本体查询 / 语义检索 | [`_generated/ontology-query.md`](_generated/ontology-query.md) |
| 🟨 vega-backend | [`vega/`](vega/) | 数据可观测：Catalog / 资源 / 连接器 / 构建任务 / 发现任务 / 原生查询 | [`_generated/vega.md`](_generated/vega.md) |

> 待补写模块（各自独立 PR）：`context-loader`、`execution-factory`、`bkn-safe`、`mf-model`。

## 🔗 共享定义

`_shared/` 收敛跨模块复用的 schema，各模块 YAML 用 `$ref` 引用，不再各自内嵌：

| 文件 | 内容 |
|---|---|
| [`_shared/errors.yaml`](_shared/errors.yaml) | 统一错误响应体（Go 服务 `rest.BaseError`：`error_code / description / solution / error_link / error_details`）。引用：`$ref: '../_shared/errors.yaml#/components/schemas/Error'` |
| [`_shared/auth.yaml`](_shared/auth.yaml) | 认证方案（OAuth2 clientCredentials + AppKey `bak_`）。引用：`$ref: '../_shared/auth.yaml#/components/securitySchemes/OAuth2'` |

> ⚠️ mf-model 是 FastAPI，错误信封字段不同（`code / detail / link`），补写时单列 `errors-fastapi.yaml`，不并入上面这套——不假装全平台一套错误结构。

## 🛠️ 渲染管线

Markdown 是**产物**，不要手改。改 YAML 后由 CI 自动重渲并提交回分支；本地也可手动跑：

```bash
npm install            # 安装 widdershins + @redocly/cli（根 package.json）
make api-docs-lint     # 校验 OpenAPI YAML（$ref 可解析等）
make api-docs          # YAML → Markdown，输出到 _generated/（进 git）
make api-docs-html     # YAML → 交互式 HTML，输出到 _generated/html/（不进 git）
```

- **两种产物**：`_generated/*.md` 是 Markdown（进 git，GitHub / 飞书直读）；`_generated/html/` 是 redocly 渲染的**交互式 HTML**（带搜索 / 折叠 / 示例，体积大，**不进 git**，本地用 `make api-docs-html` 生成后打开 `_generated/html/index.html` 查看）。
- **CI**：[`.github/workflows/ci-docs-api.yml`](../../.github/workflows/ci-docs-api.yml)。PR 触碰 `docs/api/**` 时 lint + 渲染 md，若 `_generated/` 与源不同步则自动提交最新 md；push 到 `main` 后渲染 HTML 并发布到 **GitHub Pages**（在线查看，需仓库 Settings → Pages 把 Source 设为 “GitHub Actions”）。
- **Lint 配置**：[`.redocly.yaml`](../../.redocly.yaml)。底线是 `$ref` 可解析；example/描述类既存瑕疵降为 warn，留各模块补写时清理。

## ✍️ 约定

> 编写规则见 [`rules/CONTRIBUTING.zh.md`](../../rules/CONTRIBUTING.zh.md) 的「文档放置规范」一节。下面是要点：

- 新增 / 修改 API 文档 → 改对应模块的 `*.yaml`，一资源一 YAML。
- 跨模块复用的错误 / 认证 → 引 `_shared/`，不复制。
- 旧位置 `adp/docs/api/` 只留 [`MOVED.md`](../../adp/docs/api/MOVED.md) 指针，不再放文件。
