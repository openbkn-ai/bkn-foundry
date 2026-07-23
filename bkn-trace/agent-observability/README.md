# agent-observability

基于参考项目 `agent-factory` 的六边形架构实现的 Agent Trace 查询服务。

当前提供：
- Trace 原始 DSL 查询接口：`POST /api/agent-observability/v1/traces/_search`
- Conversation 维度包装查询接口：`GET /api/agent-observability/v1/traces/by-conversation?conversation_id=...`
- Evidence 事件接收接口：`POST /api/agent-observability/v1/evidence/events`
- Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/{trace_id}/evidence-chain`
- Request 维度 Evidence Chain 查询接口：`GET /api/agent-observability/v1/traces/by-request?request_id=...`
- OpenSearch 查询客户端
- 阶段二 Evidence ingestion 校验、归一化和可替换存储接口
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

阶段二 evidence ingestion 接口接受 `bkn.trace.schema.version=2.0.0` 的事件批次，包含 `trace` 与 `events`。当前版本先完成 contract 校验、敏感 payload 拒绝、归一化计数、内存 repository 写入和最小 Evidence Chain 查询；后续 PR 会把 repository 替换为持久化 evidence index。

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
