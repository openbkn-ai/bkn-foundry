# ✍️ API 文档编写规则

本目录（`docs/api/`）统一收纳各服务的 OpenAPI 文档。**YAML 是唯一真相源**，Markdown 由工具从 YAML 渲染，不手写、不手改。新增或修改 API 文档时遵循以下规则。

## 📁 目录与命名

```
docs/api/
├── README.md            总索引
├── AUTHORING.md         本文件
├── _shared/             跨模块复用的 $ref 片段（errors.yaml / auth.yaml）
├── _generated/          渲染产物（md），由 CI 维护，勿手改
└── <module>/            一个模块一个目录，目录内一资源一 YAML
    ├── <resource-a>.yaml
    └── <resource-b>.yaml
```

- **一个模块一个目录**：目录名用模块名（`bkn` / `vega` / `ontology-query` / `dataflow` / …），不带 `-backend` / `-api` 之类后缀。
- **一资源一 YAML**：每个资源（或子域）单独一个 `<resource>.yaml`，文件名用 kebab-case。
- 新增模块时，在 [`README.md`](README.md) 的模块表加一行。

## 🔗 共享 schema —— 不要各自内嵌

跨模块复用的定义收敛在 [`_shared/`](_shared/)，各 YAML 用 `$ref` 引用：

| 复用项 | 引用方式 |
|---|---|
| 错误响应体 | `$ref: '../_shared/errors.yaml#/components/schemas/Error'` |
| 认证方案 | `$ref: '../_shared/auth.yaml#/components/securitySchemes/OAuth2'` |

- **错误信封按真实响应写**。Go 服务（bkn / vega / dataflow / context-loader / execution-factory / bkn-safe）统一走 `kweaver-go-lib/rest.BaseError`：`error_code / description / solution / error_link / error_details`。不要按理想契约杜撰字段。
- **不要在各 YAML 里再内嵌一份 `Error` schema**。已有的重复定义在迁移时已收敛为 `$ref`。
- 跨文件引用同模块内其它 YAML 的 schema 用相对路径（如 `./object-type.yaml#/components/schemas/ID`）；引用别的模块用 `../<module>/<file>.yaml#/...`。

> ⚠️ **mf-model 例外**：mf-model 是 FastAPI，错误信封字段不同（`code / detail / link`）。补写时单列 `errors-fastapi.yaml`，**不要**并入上面这套，也不要假装全平台一套错误结构。

## 🏷️ 内外接口与鉴权标注

- 内网接口与对外接口在同一 YAML 内区分清楚（沿用各模块现有约定，如 vega 的 `README.md` 说明）。
- 有鉴权分层的模块（如 bkn-safe 的 `/authz` / `/me` / `/admin`），在对应 path/operation 上用 `security:` 明确标注，并在 operation 描述里说明鉴权口径。

## 🛠️ 渲染与校验

Markdown 是产物，改 YAML 后由 CI（[`.github/workflows/ci-docs-api.yml`](../../.github/workflows/ci-docs-api.yml)）自动重渲并提交回分支。本地也可手动跑：

```bash
npm install            # 安装 widdershins + @redocly/cli（根 package.json）
make api-docs-lint     # 校验 OpenAPI YAML（重点：$ref 可解析）
make api-docs          # YAML → Markdown，输出到 _generated/
```

- **底线是 `$ref` 可解析**（[`.redocly.yaml`](../../.redocly.yaml) 里 `no-unresolved-refs` 为 error）。跨文件引用搬错路径、目标 schema 不存在，lint 直接红。
- example / 描述类的既存瑕疵降为 warn，不阻断；补写模块时顺手清理。
- 渲染用 `widdershins --code`，**不生成多语言代码示例**（PHP / Ruby / … 对 REST 参照是噪声）。

## ✅ 提交前自查

- [ ] 改的是 `<module>/*.yaml`，不是 `_generated/` 里的 md。
- [ ] 复用的错误 / 认证走 `_shared/` 的 `$ref`，没有重新内嵌。
- [ ] `make api-docs-lint` 通过（`$ref` 全部可解析）。
- [ ] 新增模块已在 [`README.md`](README.md) 模块表登记。
- [ ] 旧位置 `adp/docs/api/` 不再放文件（只留 [`MOVED.md`](../../adp/docs/api/MOVED.md) 指针）。
