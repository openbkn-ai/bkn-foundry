# 已迁移

本目录下的 OpenAPI 文档（vega / bkn / ontology-query）已统一迁移到仓库顶层：

**新位置：[`docs/api/`](../../../docs/api/)**

| 原路径 | 新路径 |
|---|---|
| `adp/docs/api/bkn/bkn-backend-api/` | `docs/api/bkn/` |
| `adp/docs/api/bkn/ontology-query-ai/` | `docs/api/ontology-query/` |
| `adp/docs/api/vega/vega-backend-api/` | `docs/api/vega/` |

共享的错误响应体与认证方案收敛到 [`docs/api/_shared/`](../../../docs/api/_shared/)，各 YAML 用 `$ref` 引用。

渲染出的 Markdown 版本见 [`docs/api/_generated/`](../../../docs/api/_generated/)。

> 新增或修改 API 文档请直接改 `docs/api/<模块>/*.yaml`（YAML 为唯一真相源），不要再往此目录放文件。
