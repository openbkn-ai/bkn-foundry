# agent-retrieval 接口文档

**对外接口（`/api/agent-retrieval/v1`）的 OpenAPI 文档已迁到仓库顶层的文档中心：**
[`docs/api/context-loader/`](../../../../../docs/api/context-loader/)。

按 [`rules/CONTRIBUTING.md`](../../../../../rules/CONTRIBUTING.md) 的「文档放置规范」，
各服务的 OpenAPI 文档统一放顶层 `docs/api/`，不再放在模块自己的 `docs/` 下——改对外
接口文档请改那边，本目录不要再新增对外接口的 YAML。

本目录余下的文件是**内部接口面（`/api/agent-retrieval/in/v1`）** 的草稿，鉴权走
`X-Account-ID` / `X-Account-Type` 头而非 Token，不对外发布，也不进文档站：

| 目录 | 内容 |
|---|---|
| `api_private/` | 内部面各端点的请求 / 响应定义 |
| `api_public/` | 对外面的历史草稿，已被文档中心取代，保留仅供比对 |
