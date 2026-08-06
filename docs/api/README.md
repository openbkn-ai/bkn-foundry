# 📚 API 文档

本目录统一收纳 bkn-foundry 各服务的 **OpenAPI 文档**。YAML 是统一发布格式；手写模块以 YAML 为真相源，生成型模块以源码注解为真相源。交互式 HTML 由工具从 YAML 自动渲染。

## 👀 如何查看

- **在线（推荐）**：合并到 `main` 后由 CI 发布到 **GitHub Pages**，带版本下拉、按模块的交互式文档（搜索 / 折叠 / 示例）与认证说明，一个链接看全部。
- **本地生成交互式 HTML**：

  ```bash
  npm install          # 首次：装 @redocly/cli 等文档工具
  make api-docs-html   # 渲染到 _generated/html/，打开 index.html 查看
  ```

## 🔑 如何调用（认证）

接口需认证，请求头带 `Authorization: Bearer <token>`。获取 token：**① CLI 登录**（`openbkn auth login`，token 存 `~/.bkn/` 自动携带）；**② AppKey**（`POST /api/safe/v1/me/api-keys` 签发 `bak_` 密钥，适合自动化）；**③ 应用集成设备码流**（自研应用引导用户登录，`POST /oauth2/device/auth`，无需注册 client）。完整示例见在线文档首页的「认证」区块。

## 🗂️ 模块一览

| 模块 | 目录 | 覆盖情况 |
|---|---|---|
| 🟦 bkn-backend | [`bkn/`](bkn/) | 业务知识网络：对象类 / 关系类 / 行动类 / 概念组 / 指标 / 导入导出。**全量** |
| 🟩 ontology-query | [`ontology-query/`](ontology-query/) | 本体查询 / 语义检索 / 行动执行与日志。**全量** |
| 🟨 vega-backend | [`vega/`](vega/) | 数据可观测：目录 / 资源 / 连接器 / 构建任务 / 发现任务 / 原生查询。**全量** |
| 🟪 bkn-agent | [`bkn-agent/`](bkn-agent/) | Agent 运行时：agent CRUD / 对话 / 任务 / 提示词 / 导入导出。**全量** |
| 🟥 agent-observability | [`agent-observability/`](agent-observability/) | BKN Trace：受管会话生命周期、业务证据、技术链路与快照。由 Go 注解生成，**禁止手改 YAML**。**全量** |
| 🟫 context-loader | [`context-loader/`](context-loader/) | Agent 上下文入口：Schema 检索 / 实例与子图查询 / 逻辑属性 / 行动执行 / Skill 召回 / 数据直查 / MCP。**外部面全量**（内部 `/in/v1` 面不收录） |
| 🟩 execution-factory | [`execution-factory/`](execution-factory/) | 执行工厂：函数 / 沙箱观测 / 导入导出 / 算子 / MCP / 工具箱 / Skill。**公开面全量**（89 个端点）。只收 Ingress 暴露的 `/v1`，内部面 `internal-v1` 刻意不收（不校验令牌），能力面 `/api/capabilities-lab/v1` 暂未收 |
| 🟧 mf-model-manager | [`mf-model-manager/`](mf-model-manager/) | 模型工厂。**仅部分**：目前只覆盖大模型的连通性测试、默认模型设置与用量总览，其余接口（小模型、配额、提示词等）尚未文档化 |
| 🟦 bkn-safe | [`bkn-safe/`](bkn-safe/) | BKN Trace 依赖的自助知识网络授权范围读取。**部分** |

> `bkn-safe` 仅收录已由外部运行时依赖的自助读取接口，不将管理面 API 误作为通用集成合同。

### ⚠️ `/api/ontology-manager/v1` 是历史别名，不要再用

bkn-backend 同时注册了 `/api/bkn-backend/v1` 与 `/api/ontology-manager/v1`
两套外部路由，逐条等价（内部面的 `in/v1` 同理）。后者是 monorepo 重构
（#111）时为兼容旧调用方保留的别名，helm ingress 至今仍暴露它。

**规范前缀是 `/api/bkn-backend/v1`**，本文档只按它编写：

- 仓库内的服务调用一律走 `/api/bkn-backend/v1`（128 处），无一处使用别名；
- 唯一残留的使用方是 examples 脚本，已切到规范前缀；
- 别名路由暂不下线，避免破坏存量客户端；待确认外部无调用后再移除。

## 🔗 共享定义

`_shared/` 收敛跨模块复用的 schema，各模块 YAML 用 `$ref` 引用，不再各自内嵌：

| 文件 | 内容 |
|---|---|
| [`_shared/errors.yaml`](_shared/errors.yaml) | 统一错误响应体（Go 服务 `rest.BaseError`：`error_code / description / solution / error_link / error_details`）。引用：`$ref: '../_shared/errors.yaml#/components/schemas/Error'` |
| [`_shared/auth.yaml`](_shared/auth.yaml) | 认证方案（OAuth2 clientCredentials + AppKey `bak_`）。引用：`$ref: '../_shared/auth.yaml#/components/securitySchemes/OAuth2'` |

> ⚠️ mf-model 是 FastAPI，错误信封字段不同（`code / detail / link`），补写时单列 `errors-fastapi.yaml`，不并入上面这套——不假装全平台一套错误结构。

## 🛠️ 渲染管线

`_generated/` 下全部是**渲染产物**，不进 git、不要手改。本地手动跑：

```bash
npm install            # 安装 @redocly/cli + widdershins（根 package.json）
make api-docs-lint     # 校验 OpenAPI YAML（$ref 可解析等）
make api-docs-html     # YAML → 交互式 HTML，输出到 _generated/html/
make api-docs          # （可选）YAML → Markdown，输出到 _generated/*.md，本地阅读 / 喂飞书用
```

- **CI**：[`.github/workflows/ci-docs-api.yml`](../../.github/workflows/ci-docs-api.yml)。PR 触碰 `docs/api/**` 时 lint；push 到 `main` 后渲染 HTML 并发布到 **GitHub Pages**（在线查看，需仓库 Settings → Pages 把 Source 设为 “GitHub Actions”）。
- **Lint 配置**：[`.redocly.yaml`](../../.redocly.yaml)。底线是 `$ref` 可解析；example/描述类既存瑕疵降为 warn，留各模块补写时清理。

## ✍️ 约定

> 编写规则见 [`rules/CONTRIBUTING.zh.md`](../../rules/CONTRIBUTING.zh.md) 的「文档放置规范」一节。下面是要点：

- 新增 / 修改 API 文档 → 改对应模块的 `*.yaml`，一资源一 YAML。
- `agent-observability` 是生成型例外：修改 Go 注解后执行 `make -C bkn-trace/agent-observability gen-swag`，不得直接编辑发布 YAML；`check-swag` 会校验运行时 JSON、Go 文档与发布 YAML 一致。
- 跨模块复用的错误 / 认证 → 引 `_shared/`，不复制。
- 旧位置 `adp/docs/api/` 只留 [`MOVED.md`](../../adp/docs/api/MOVED.md) 指针，不再放文件。
