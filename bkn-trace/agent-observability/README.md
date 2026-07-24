# agent-observability

基于参考项目 `agent-factory` 的六边形架构实现的 Agent Trace 查询服务。

当前提供：
- Trace 原始 DSL 查询接口：`POST /api/agent-observability/v1/traces/_search`
- Conversation 维度包装查询接口：`GET /api/agent-observability/v1/traces/by-conversation?conversation_id=...`
- Trace Graph 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/trace-graph`
- Evidence 事件接收接口：`POST /api/agent-observability/v1/evidence/events`
- Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/evidence-chain`
- Request 维度 Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/by-request?request_id=...`
- Business Graph 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/business-graph`
- Request 维度 Business Graph 查询接口：`GET /api/agent-observability/v1/traces/by-request/business-graph?request_id=...`
- Evidence Node 查询接口：`GET /api/agent-observability/v1/evidence-nodes/{node_id}?trace_id=...`
- OpenSearch 查询客户端
- 阶段二 Evidence ingestion 校验、归一化和可替换存储接口，支持内存 store 与 OpenSearch evidence index store
- Swagger 文档生成
- Docker 镜像构建
- Helm Deployment Chart
- GitHub Actions 构建与发布流水线

## Development

本地测试：

```bash
make test
```

仅测试 BKN Trace 服务：

```bash
GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...
```

阶段二 evidence ingestion 接口接受 `bkn.trace.schema.version=2.0.0` 的事件批次，包含 `trace` 与 `events`。当前版本先完成 contract 校验、敏感 payload 拒绝、归一化计数、最小 Evidence Chain 查询、Business Graph 查询和 Evidence Node 查询；默认使用内存 repository，生产或共享测试环境可切换到 OpenSearch evidence index store。

### Evidence Store

默认配置保持兼容：

```text
BKN_TRACE_EVIDENCE_STORE=memory
```

启用 OpenSearch 持久化 evidence index：

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set evidence.store=opensearch \
  --set evidence.index=bkn-trace-evidence-v1 \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  -n observability --create-namespace
```

对应环境变量：

```text
BKN_TRACE_EVIDENCE_STORE=opensearch
OPENSEARCH_EVIDENCE_INDEX=bkn-trace-evidence-v1
```

默认部署不自动创建 index，不要求服务账号具备 OpenSearch index-management 权限；部署方需要提前创建 `OPENSEARCH_EVIDENCE_INDEX`。

如果部署环境允许 Helm pre-install/pre-upgrade hook 创建 OpenSearch index，可以显式启用最小 index setup：

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set evidence.store=opensearch \
  --set evidence.index=bkn-trace-evidence-v1 \
  --set evidence.indexManagement.enabled=true \
  --set evidence.indexManagement.createJob.enabled=true \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  -n observability --create-namespace
```

启用后 Chart 会渲染 evidence index mapping ConfigMap，并在 index 不存在时由 hook Job 创建 index。最小 mapping 将 `trace_id`、`bkn.request.id`、`document_id` 等查询字段设为 `keyword`，将 `ingested_at` 设为 `date`，并把 `events` 保留在 `_source` 中但不展开索引，避免 event payload 动态字段膨胀。retention/ILM、细粒度权限、迁移脚本仍属于后续部署治理能力。

Evidence Chain 与 Business Graph 查询支持可选 `limit` 参数，限制本次读取的 evidence trace 批次数：

```http
GET /api/agent-observability/v1/traces/{trace_id}/evidence-chain?limit=100
GET /api/agent-observability/v1/traces/by-request/business-graph?request_id=req_x&limit=100
```

`limit` 取值范围为 `1..1000`，默认 `1000`。命中上限时响应会返回 `partial=true`、`partial_reason=["evidence_query_truncated"]`，并设置 `page.truncated=true`，调用方不得把该结果展示为完整证据链。

Trace Graph 查询把 OTel spans 归一化为 trace tree：

```http
GET /api/agent-observability/v1/traces/{trace_id}/trace-graph
```

```json
{
  "trace_id": "9c0d...",
  "status": "error",
  "duration_nano": 110,
  "partial": false,
  "partial_reason": [],
  "page": {
    "node_count": 3,
    "edge_count": 2,
    "truncated": false
  },
  "data": {
    "nodes": [
      {
        "span_id": "root",
        "name": "POST /chat",
        "kind": "SERVER",
        "service_name": "bkn-agent",
        "status": "ok",
        "start_nano": 100,
        "end_nano": 210,
        "duration_nano": 110
      }
    ],
    "edges": [
      {
        "id": "edge:1",
        "parent_span_id": "root",
        "child_span_id": "child",
        "edge_type": "parent_child"
      }
    ]
  }
}
```

当 span 指向缺失父节点时，Trace Graph 不生成悬空边，并返回 `partial=true`、`partial_reason=["orphan_span"]`。

### Evidence 写入安全边界

`POST /api/agent-observability/v1/evidence/events` 是写接口，生产环境必须通过平台网关鉴权保护，或配置服务内最小 ingest token：

```bash
kubectl create secret generic bkn-trace-evidence-ingest \
  --from-literal=token='<strong-token>' \
  -n observability
```

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set evidence.ingestAuth.existingSecret=bkn-trace-evidence-ingest \
  -n observability
```

启用后，写入方需要携带 `Authorization: Bearer <strong-token>` 或 `X-BKN-Trace-Ingest-Token: <strong-token>`。未配置 `BKN_TRACE_EVIDENCE_INGEST_TOKEN` 时保持当前兼容行为，依赖平台网关/网络边界保护。

当前阶段的 Evidence Chain 查询依据事件生产方或 resolver 声明的 `visibility` 做响应过滤，并区分 `redacted`、`hidden`、`omitted`、`unresolved`、`unauthorized` 统计。`unauthorized` 引用只进入汇总和 `partial_reason[]`，不会展开 `ref_id`、`policy_decision_ref` 或其他节点详情。按调用者身份实时裁决、授权审计和 resolver-backed 节点详情补全仍属于后续阶段。

Evidence Chain 查询返回稳定 envelope：

```json
{
  "trace_id": "9c0d...",
  "bkn.request.id": "req_handler_001",
  "partial": false,
  "partial_reason": [],
  "visibility_summary": {
    "authorized_ref_count": 2,
    "redacted_ref_count": 0,
    "hidden_ref_count": 0,
    "omitted_ref_count": 0,
    "unresolved_ref_count": 0,
    "unauthorized_ref_count": 0
  },
  "page": {
    "next_cursor": null,
    "node_count": 3,
    "edge_count": 2,
    "truncated": false
  },
  "data": {
    "claims": [],
    "evidence_refs": [],
    "business_refs": []
  }
}
```

Business Graph 查询返回从 `business.refs.resolved` 派生的业务语义图：

```json
{
  "trace_id": "9c0d...",
  "bkn.request.id": "req_handler_002",
  "partial": false,
  "partial_reason": [],
  "visibility_summary": {
    "authorized_ref_count": 0,
    "redacted_ref_count": 0,
    "hidden_ref_count": 0,
    "omitted_ref_count": 0,
    "unresolved_ref_count": 0
  },
  "page": {
    "next_cursor": null,
    "node_count": 2,
    "edge_count": 1,
    "truncated": false
  },
  "data": {
    "nodes": [
      {
        "id": "claim:claim_handler_business",
        "node_type": "claim",
        "claim_id": "claim_handler_business"
      },
      {
        "id": "business:object:customer",
        "node_type": "object",
        "label": "Customer"
      }
    ],
    "edges": [
      {
        "id": "edge:1",
        "source_id": "claim:claim_handler_business",
        "target_id": "business:object:customer",
        "edge_type": "claim_to_object"
      }
    ]
  }
}
```

Business Graph 当前只消费已进入 BKN Trace 的 `business_refs`，并复用 `visibility` 做响应过滤。`unresolved` 与 `unauthorized` 会分别进入 `visibility_summary` 和 `partial_reason[]`，不会生成业务节点或边。真实 BKN / Vega / Metric / Action resolver、按账号/租户的实时授权裁决和 resolver-backed 节点详情补全属于后续阶段。

Evidence Node 查询用于打开单个可见节点详情：

```http
GET /api/agent-observability/v1/evidence-nodes/claim%3Aclaim_handler?trace_id=9c0d...
GET /api/agent-observability/v1/evidence-nodes/business_ref%3Aobject%3Acustomer?request_id=req_handler_002
```

首版 node id 格式：

```text
claim:{claim_id}
evidence_ref:{ref_id}
business_ref:{ref_id}
```

查询必须提供且只能提供一个 scope：`trace_id` 或 `request_id`。当前阶段只返回 `visibility=visible` 的节点；`hidden`、`redacted`、`omitted`、`unresolved`、`unauthorized` 节点不会通过详情接口展开。真实 BKN / Vega / Metric / Action resolver、按账号/租户的实时授权裁决和隐藏节点可审计解释属于后续阶段。

生成 Swagger 文档：

```bash
make gen-swag
```

查看 Swagger 文档地址：

```bash
make view-swag
```

服务启动后可访问：

```text
http://localhost:8080/api/agent-observability/v1/swagger/swagger.json
http://localhost:8080/api/agent-observability/v1/swagger/swagger.yaml
```

## Docker

本地构建镜像：

```bash
make docker-build
```

默认镜像名：

```text
swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability:local
```

也可以覆盖：

```bash
make docker-build IMAGE=swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability:v0.1.1
```

## Helm

Chart 目录：

```text
charts/agent-observability
```

本地校验：

```bash
make helm-lint
```

打包：

```bash
make helm-package
```

安装示例：

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set image.repository=swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability \
  --set image.tag=0.1.1 \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  --set opensearch.auth.enabled=false \
  -n observability --create-namespace
```

启用 OpenSearch Basic Auth：

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set image.repository=swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability \
  --set image.tag=0.1.1 \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  --set opensearch.auth.enabled=true \
  --set opensearch.auth.username=your-username \
  --set opensearch.auth.password=your-password \
  -n observability --create-namespace
```

## CI/CD

GitHub Actions 工作流位于：

```text
.github/workflows/release-agent-observability.yml
```

分为三个阶段：
- `test-and-lint`：执行 `go test ./...` 和 `golangci-lint`
- `build-and-push-image`：构建并推送 `linux/amd64`、`linux/arm64` 镜像到 SWR
- `package-and-push-chart`：打包 Helm chart 并推送到 `ghcr.io`

当前默认镜像仓库：

```text
swr.cn-east-3.myhuaweicloud.com/kweaver-ai/agent-observability
```

需要配置的 GitHub Secrets：
- `SWR_USERNAME`
- `SWR_PASSWORD`

Chart 会推送到：

```text
ghcr.io/<github-owner>/charts
```
# test trigger
