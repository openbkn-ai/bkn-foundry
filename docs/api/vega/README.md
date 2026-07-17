# Vega Backend API 文档

> Vega Backend HTTP API 的 OpenAPI 3.1.1 定义。每个文件聚焦一个资源概念；跨资源的"便利端点"按"按谁过滤就归谁"的原则归属到对应资源文件，避免 schema 重复定义和文档撕裂。

## 文件索引

| 文件 | 资源 | 包含的端点 |
|---|---|---|
| [auth-resource.yaml](auth-resource.yaml) | AuthResource | `GET /auth-resources`（按 `resource_type` 获取可授权资源） |
| [catalog.yaml](catalog.yaml) | Catalog | `GET/POST /catalogs`、`GET/PUT/DELETE /catalogs/{id(s)}`、`POST .../enable`、`POST .../disable`、`GET /catalogs/{id}/health-status`、`POST /catalogs/{id}/test-connection` |
| [connector-type.yaml](connector-type.yaml) | ConnectorType | `GET/POST /connector-types`、`GET/PUT/DELETE /connector-types/{type}`、`POST .../enable`、`POST .../disable` |
| [discover-task.yaml](discover-task.yaml) | DiscoverTask | `POST /catalogs/{id}/discover`、`GET /discover-tasks`、`GET /discover-tasks/{id}`、`DELETE /discover-tasks/{ids}` |
| [discover-schedule.yaml](discover-schedule.yaml) | DiscoverSchedule | `GET/POST /discover-schedules`、`GET/PUT/DELETE /discover-schedules/{id}`、`POST .../enable`、`POST .../disable` |
| [resource.yaml](resource.yaml) | Resource | `GET/POST /resources`、`GET/PUT/DELETE /resources/{id(s)}` |
| [resource-data.yaml](resource-data.yaml) | ResourceData | `POST/PUT /resources/{id}/data`、`GET/PUT/DELETE /resources/{id}/data/{doc_id(s)}` |
| [raw-query.yaml](raw-query.yaml) | RawQuery | `POST /resources/query` |
| [build-task.yaml](build-task.yaml) | BuildTask | `GET/POST /build-tasks`、`GET/DELETE /build-tasks/{id(s)}`、`POST .../start`、`POST .../stop` |
| [semantic-understanding-task.yaml](semantic-understanding-task.yaml) | SemanticUnderstandingTask | `GET/POST /semantic-understanding-tasks`、`GET/DELETE /semantic-understanding-tasks/{id(s)}` |

## 约定

- **OpenAPI 版本**：3.1.1。
- **错误响应**：所有非 2xx 响应统一返回 `Error` schema（对应 `kweaver-go-lib/rest.BaseError`），各文件自含一份定义。
- **内部接口**：多数业务端点同时提供 `/api/vega-backend/in/v1/...`，与对应外部端点的请求 / 响应结构一致；区别仅在鉴权方式（外部 OAuth Token，内部 Header `X-Account-ID` / `X-Account-Type`）。本文档仅描述外部接口；请以 `driveradapters/router.go` 中实际注册的内部路由为准。
- **跨资源动作端点**：`POST /catalogs/{id}/discover` 创建 DiscoverTask 实例，定义在 `discover-task.yaml`。
- **Catalog / Resource `extensions`（Issue #382，方案 B）**：OpenAPI 定义在 [catalog.yaml](catalog.yaml)、[resource.yaml](resource.yaml)。请求/响应与列表投影仅 **`extensions`**；列表筛选 query 为 **`extension_key` / `extension_value`**；`include_extensions`、`include_extension_keys` 见两文件。持久化表 **`t_entity_extension`** 及约定见 `info.description` 与设计稿
  [catalog-resource-labels-scheme-b-design.md](../../../design/vega/features/vega-backend/dip-for-extension/catalog-resource-labels-scheme-b-design.md)。
