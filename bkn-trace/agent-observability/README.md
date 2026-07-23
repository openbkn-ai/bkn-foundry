# agent-observability

基于参考项目 `agent-factory` 的六边形架构实现的 Agent Trace 查询服务。

当前提供：
- Trace 原始 DSL 查询接口：`POST /api/agent-observability/v1/traces/_search`
- Conversation 维度包装查询接口：`GET /api/agent-observability/v1/traces/by-conversation?conversation_id=...`
- Evidence 事件接收接口：`POST /api/agent-observability/v1/evidence/events`
- Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/evidence-chain`
- Request 维度 Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/by-request?request_id=...`
- Business Graph 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/business-graph`
- Request 维度 Business Graph 查询接口：`GET /api/agent-observability/v1/traces/by-request/business-graph?request_id=...`
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

阶段二 evidence ingestion 接口接受 `bkn.trace.schema.version=2.0.0` 的事件批次，包含 `trace` 与 `events`。当前版本先完成 contract 校验、敏感 payload 拒绝、归一化计数、最小 Evidence Chain 查询和 Business Graph 查询；默认使用内存 repository，生产或共享测试环境可切换到 OpenSearch evidence index store。

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

当前 PR 不自动创建 index，不要求服务账号具备 OpenSearch index-management 权限；部署方需要提前创建 `OPENSEARCH_EVIDENCE_INDEX`。后续部署治理 PR 会补 index mapping、retention/ILM、权限与迁移脚本。

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

当前阶段的 Evidence Chain 查询只依据事件生产方声明的 `visibility` 做响应过滤，不按调用者身份做细粒度可见性裁决。resolver-backed authorization、按账号/租户的 evidence ref 授权与审计将在后续持久化 evidence index 阶段接入。

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
    "unresolved_ref_count": 0
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

Business Graph 当前只消费已进入 BKN Trace 的 `business_refs`，并复用 `visibility` 做响应过滤。真实 BKN / Vega / Metric / Action resolver、按账号/租户的授权裁决、节点详情展开和持久化索引属于后续阶段。

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
