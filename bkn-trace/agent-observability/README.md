# agent-observability

基于参考项目 `agent-factory` 的六边形架构实现的 Agent Trace 查询服务。

当前提供：
- 类型化 Trace 列表接口：`GET /api/agent-observability/v1/traces`
- 单 Trace 技术详情接口：`GET /api/agent-observability/v1/traces/{trace_id}`
- Evidence 事件接收接口：`POST /api/agent-observability/v1/evidence/events`
- 企业 Evidence Chain 查询接口：`GET /api/agent-observability/v1/business-provenance/traces/{trace_id}/evidence-chain`
- 企业 Request Evidence Chain 查询接口：`GET /api/agent-observability/v1/business-provenance/requests/{request_id}/evidence-chain`
- 企业 Business Graph 查询接口：`GET /api/agent-observability/v1/business-provenance/traces/{trace_id}/business-graph`
- 企业 Request Business Graph 查询接口：`GET /api/agent-observability/v1/business-provenance/requests/{request_id}/business-graph`
- Evidence Node 查询接口（企业业务溯源）：`GET /api/agent-observability/v1/business-provenance/evidence-nodes/{node_id}?trace_id=...`
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

本地 E2E Lite 就绪性探测：

```bash
python3 scripts/test_bkn_trace_e2e_lite_probe.py
python3 scripts/bkn_trace_e2e_lite_probe.py \
  --base-url http://localhost \
  --trace-id <trace_id> \
  --request-id <bkn.request.id>
```

默认模式只探测真实 OpenBKN Gateway / BKN Trace API 是否可用，不写入集群、不创建索引、不使用 mock 数据。常见失败原因包括：Studio dev proxy 未启用、认证失败、BKN Trace API 未部署、trace/evidence OpenSearch index 缺失或 trace id 不存在。

如果本地已经有 OTLP HTTP collector 和 BKN Trace API，可显式启用写入模式，生成一条最小真实 trace 和最小 evidence batch 后再查询：

```bash
python3 scripts/bkn_trace_e2e_lite_probe.py \
  --base-url http://127.0.0.1:8080 \
  --trace-id 33333333333333333333333333333333 \
  --request-id req_bkn_trace_e2e_lite_probe_003 \
  --emit-otlp-url http://127.0.0.1:4318/v1/traces \
  --ingest-evidence
```

本地自签 HTTPS 网关可以加 `--insecure`。写入模式仍只写最小诊断 span、claim、evidence ref 和 business ref，不包含 prompt、完整 SQL、行级数据、token 或裸对象存储 URL。由于 collector 到 OpenSearch 可能存在短暂可见性延迟，查询类 endpoint 默认重试 3 次，可用 `--retries` 和 `--retry-delay` 调整。

阶段二 evidence ingestion 接口接受 `bkn.trace.schema.version=2.0.0` 的事件批次，包含 `trace` 与 `events`。当前版本先完成 contract 校验、敏感 payload 拒绝、归一化计数、最小 Evidence Chain 查询、Business Graph 查询和 Evidence Node 查询；默认使用内存 repository，生产或共享测试环境可切换到 OpenSearch evidence index store。

### BKN Trace 3.0 Interaction 业务语义图

`POST /api/agent-observability/v1/evidence/events` 与 `GET /api/agent-observability/v1/interactions/{interaction_id}/business-graph` 使用 BKN Trace 3.0 强类型合同。3.0 是 0.1.3 的新基线，不兼容未发布的旧请求体：事件必须使用 `bkn.trace.schema.version`，归属只能来自可信身份上下文，请求体中的 `schema_version` 和 `tenant_id` 会被拒绝。

Interaction 业务语义图遵循以下口径：

- `adopted_supports`、`rejected_supports` 和 `unused_evidence_refs` 互斥；Claim 级 unused 表示既未被该 Claim 采纳、也未被该 Claim 拒绝，全局 unused 表示未被本修订任何 Claim 分类。
- `claim_status=withdrawn` 保留历史 Claim 及其支持关系，但不参与当前修订的证据完备性判断。
- 相同支持合同命中多个版本、时间点或证据类型时，不任意选择，返回 `support_target_ambiguous` 并标记客观证据不完整。
- `completeness` / `partial_reasons` 描述客观证据组装；`disclosure_partial` / `disclosure_reasons` 描述当前用户授权投影。resolver 不可用、未配置或无法确认权限时，业务节点及其操作边默认不披露。
- resolver 按 `ref_type + ref_id + source_system` 判定，不使用不匹配的 RefID 前缀推断权限。暂时没有安全实例级授权接口的类型保持 `unresolved`，不能由父类型权限推断实例权限。

`ref_type` 与 `ref_id` 的规范结构是一一对应的写入合同。事件与 operation receipt 两条写入路径都必须使用完整限定格式；前缀、段数或版本不合法时会拒绝写入，避免业务节点在查询时静默消失。

| `ref_type` | `ref_id` 规范结构 |
|---|---|
| `knowledge_network` | `kn:<kn_id>` |
| `object_type` | `object:<kn_id>:<object_type_id>` |
| `object_instance` | `object_instance:<kn_id>:<object_type_id>:<opaque_instance_id>`（不透明尾段可包含 `:`） |
| `property` | `property:<kn_id>:<object_type_id>:<property_id>` |
| `relation_type` | `relation:<kn_id>:<relation_type_id>` |
| `data_resource` | `resource:<resource_id>` |
| `metric` | `metric:<kn_id>:<metric_id>` |
| `logic` | `logic:<kn_id>:<object_type_id>:<logic_id>` |
| `function` | `function:<kn_id>:<function_id>` |
| `action_type` | `action_type:<kn_id>:<action_type_id>` |
| `action_instance` | `action_instance:<kn_id>:<action_type_id>:<opaque_instance_id>`（不透明尾段可包含 `:`） |

业务名称和授权可见性解析默认关闭，因为 BKN 与 Vega 的内部服务地址因部署环境而异。关闭时业务语义图仍返回技术事件、Claim 和证据结构，但 `business_refs` 与 `operation_business_edges` 会安全返回空数组，并通过 `disclosure_partial` 标记解析能力未启用。生产环境应显式配置：

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set businessResolver.enabled=true \
  --set businessResolver.bknBaseURL=http://bkn-backend \
  --set businessResolver.vegaBaseURL=http://vega-backend \
  -n observability
```

内部服务地址需按实际命名空间和 Service 名称调整；不能访问这两个授权接口时，不应启用 resolver。

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

Evidence 与 Artifact 存储在首次读写时由运行时 `EnsureIndex` 创建或更新 mapping；Chart 不再渲染独立的索引初始化 Hook。启用 OpenSearch Evidence Store 的服务身份须具备目标索引的创建及 mapping 更新权限。

Evidence Chain 与 Business Graph 查询支持可选 `limit` 参数，限制本次读取的 evidence trace 批次数：

```http
GET /api/agent-observability/v1/business-provenance/traces/{trace_id}/evidence-chain?limit=100
GET /api/agent-observability/v1/business-provenance/requests/{request_id}/business-graph?limit=100
```

`limit` 取值范围为 `1..1000`，默认 `1000`。命中上限时响应会返回 `partial=true`、`partial_reason=["evidence_query_truncated"]`，并设置 `page.truncated=true`，调用方不得把该结果展示为完整证据链。

单 Trace 技术详情包含归一化 OTel Span 图与 Operation 调用事实：

```http
GET /api/agent-observability/v1/traces/{trace_id}
```

```json
{
  "summary": {
    "trace_id": "9c0d...",
    "request_id": "req_...",
    "status": "error"
  },
  "graph": {
    "trace_id": "9c0d...",
    "status": "error",
    "data": {
      "nodes": [{
        "span_id": "root",
        "name": "POST /chat",
        "service_name": "bkn-agent"
      }],
      "edges": []
    }
  },
  "operations": [],
  "partial": false
}
```

Trace Graph 单次最多返回 1000 个 span 节点。命中上限时服务会使用 `limit+1` 查询探测截断，响应返回 `partial=true`、`partial_reason=["trace_query_truncated"]`，并设置 `page.truncated=true`。当 span 指向缺失父节点时，Trace Graph 不生成悬空边，并返回 `partial_reason=["orphan_span"]`。当 span 时间戳缺失、非法或结束时间早于开始时间时，节点耗时会归零，整体耗时不会返回负数，并返回 `partial_reason=["invalid_span_timestamp"]`。

### Evidence 写入安全边界

受管 Conversation、Interaction、Operation 生命周期继续监听于集群内部的 `agent-observability-internal:8081`，同时在公开 8080 接口为 OAuth SDK 客户端开放。公开写入由服务端根据 OAuth 与 BKN Safe 身份派生 owner，并限制在部署方配置的 tenant 内，不接受客户端伪造 owner。Evidence Ledger 与 Artifact 保留在 8080 的已发布生产者接口，并继续校验独立的 `bkn-trace-evidence-ingest` token，以兼容 bkn-agent、Vega、BKN Backend、ontology-query 和 Context Loader。公开读取仍由 OAuth 与 Access Profile 保护。Chart 的 NetworkPolicy 默认只允许带稳定 `app.kubernetes.io/name=agent-retrieval` 标签的 Pod 访问 8081，其他部署可通过 `networkPolicy.allowedClients` 显式扩展。

该 NetworkPolicy 依赖 Kubernetes 1.23 或更高版本。离线执行 `helm template` 时应显式传入 `--kube-version 1.23.0` 或实际目标集群版本；连接集群的 `helm install/upgrade` 会按目标集群能力校验。

Chart 默认不创建或接管 `bkn-trace-evidence-ingest` Secret。OpenBKN 整体安装器在 release 循环前创建或验证该 Secret，并将同一 token 注入 Agent Observability、Context Loader、Vega、BKN Backend、Ontology Query、BKN Agent 与行动执行服务；BKN Backend 和 Ontology Query 同时启用其持久 Evidence outbox worker 与既有清理任务（已投递记录保留 30 天，放弃记录保留 180 天）。单独安装任一 Chart 时，应预先创建并显式引用该 Secret。对无 outbox 的生产者，禁用 Evidence 写入须同时清空 ingest URL 与 token Secret；对启用了 producer outbox 的 BKN Backend 和 Ontology Query，须同时关闭 outbox 与 worker，不能只清空 URL。`evidence.ingestAuth.createSecret=true` 只适用于 Helm 直接执行的首次安装，不适用于 `helm template | kubectl apply`，也不能用于接管已有的外部 Secret。

```bash
printf '%s' '<user>:<password>@tcp(<host>:3306)/<database>?parseTime=true' | \
kubectl create secret generic bkn-trace-core-mariadb \
  --from-file=dsn=/dev/stdin \
  -n observability
```

```bash
helm upgrade --install agent-observability charts/agent-observability \
  --set core.store=mariadb \
  --set core.mariadb.existingSecret=bkn-trace-core-mariadb \
  --set core.projection.enabled=true \
  -n observability
```

Context Loader 通过内部监听器携带 tenant、application principal、effective subject type/id 和可选 delegation 构成的 owner tuple。公开监听器不信任这些 owner 请求头，而是从 OAuth、BKN Safe 和部署批准的 tenant 重新构造 owner；任一授权依赖不可用时均拒绝写入，不得 fail-open。证据生产者则必须携带 ingest token；内部网络身份不能绕过该校验。

Chart 默认使用 memory store 且关闭 Core projection，以保证存量 trace/evidence 读取在普通 chart 升级时不会因缺少新 Secret 而中断。该默认值不代表具备 durable lifecycle；生产启用受管 Conversation / Interaction 时必须显式采用上面的 MariaDB 与 projection 配置。

### MariaDB Schema 版本账本与升级

启用 `core.store=mariadb` 时，服务以镜像内嵌的迁移清单管理 `bkn_trace`。数据库中的
`bkn_trace_schema_migrations` 保存已应用版本及 SHA-256 校验和；启动时服务在 MariaDB
建议锁内校验账本，再只应用缺少版本。多副本不会并发执行 DDL。

`BKN_TRACE_CORE_AUTO_MIGRATE` 对 MariaDB 默认是 `true`，并且只能取 Go 布尔值。拼写错误
会让服务在就绪前失败，不能再静默退化为“不迁移”。显式设为 `false` 时，服务仍会校验账本：
数据库缺少账本、落后于镜像、校验和不一致，或版本比镜像新都会拒绝启动并给出原因。

已发布的 `migrations/mariadb/vNNN/init.sql` 是不可变合同，不能就地追加或修改 SQL；所有
Schema 变更必须新增下一个版本目录。每条迁移中的每一句也必须可重入（例如使用
`IF EXISTS` / `IF NOT EXISTS`），因为 MariaDB DDL 在账本行写入前会隐式提交，进程中断后
服务需要安全重试该版本。

遇到旧部署没有账本且需要保留运行现场时，不要通过回滚单个镜像绕过启动失败；先完成
数据库备份并按完整升级链路验收。0.1.3 的历史 Trace/日志数据不在 0.1.4 受管事实与投影
合同内：切换验收后应按部署清理指南清除旧索引和旧 Trace 数据，再由 0.1.4 重新写入。

从旧版升级时，生命周期入口由 8080 迁移到内部 8081。自定义 agent-retrieval values 必须同步更新 `observability.lifecycle.core_url`；标准安装器会同时更新两端。两次 Helm rollout 之间旧 retrieval Pod 可能短暂访问已移除的 8080 生命周期路由，因此生产升级应在维护或流量排空窗口内完成 Observability 与 Retrieval 的连续升级，再恢复第三方 Agent 流量。NetworkPolicy 暂时同时接受旧 `app=agent-retrieval` 与新稳定标签，待所有 retrieval 工作负载完成滚动后再移除旧选择器。

安装器会把 Chart 的 `namespace` value 对齐到目标命名空间。升级前应核对 Helm release 命名空间、既有资源命名空间和历史 values；若三者存在漂移，应先明确迁移资源，避免升级时在新命名空间重建 Service 或 NetworkPolicy。

滚动升级期间，新旧 agent-retrieval 实例可能对失败调用上报不同的 evidence durability；这是升级窗口内的临时统计差异，待生产者全部完成滚动后收敛。不得据此回写或重算历史 Ledger 事件。

Studio 查询使用用户 OAuth access token。核心服务通过 `BKN_TRACE_HYDRA_ADMIN_URL` 调用 Hydra introspection，从 token 派生可信 `account_id/account_type`，拒绝客户端自报身份与 token 不一致的请求。解析 BKN/Vega 业务名称时只在内存中向授权下游转发该 Bearer，不能写入日志、事件、索引或响应。

Evidence、Business Graph、Snapshot、Node 和技术 Trace Graph 查询必须经过 OAuth 与 Access Profile 校验，并以 tenant、account 归属在 OpenSearch 条件和返回层同时过滤。跨归属查询统一返回 404，不泄露 trace 是否存在。仅本地开发和测试可显式设置 `BKN_TRACE_ALLOW_UNAUTHENTICATED_QUERY=true`，Helm 默认关闭。

统一日志分页游标使用 `BKN_OBSERVABILITY_CURSOR_SIGNING_KEY` 做 HMAC 签名，并绑定过滤条件、主体、应用、Access Profile 指纹和可见来源。多副本部署必须通过 `observability.cursorSigning.existingSecret` 为全部 Pod 注入同一密钥；单实例本地环境未配置时使用仅在当前进程有效的随机密钥，进程重启后的旧游标按失效处理。密钥不得写入日志、响应或配置文件。

升级存量环境时，如果部署 values 覆盖了 `ingress.paths`，Helm 不会合并 chart 新增路径，必须显式补充 `/api/observability/v1`。扩容到多副本前也必须先配置共享的 `observability.cursorSigning.existingSecret`，否则不同 Pod 签发的游标不能互相验证。

查询仍依据事件生产方或 resolver 声明的 `visibility` 做节点级响应过滤，并区分 `redacted`、`hidden`、`omitted`、`unresolved`、`unauthorized` 统计。`unauthorized` 引用只进入汇总和 `partial_reason[]`，不会展开 `ref_id`、`policy_decision_ref` 或其他节点详情。更细粒度的对象/属性级实时策略裁决仍属于后续阶段。

当前查询侧尚未接入受权 Resolver/display 服务，业务图节点因此只投影注册引用字段，不信任或返回事件中的 `label` 等显示信息；存在可见业务引用时返回 `partial=true` 与 `resolver_unresolved`。受权业务名称和详情补全由后续独立任务实现。

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
        "id": "business:object:kn_demo:customer",
        "node_type": "object",
        "display": {
          "name": "客户",
          "business_path": ["客户"],
          "resolution_status": "resolved",
          "source_version": "main"
        }
      }
    ],
    "edges": [
      {
        "id": "edge:1",
        "source_id": "claim:claim_handler_business",
        "target_id": "business:object:kn_demo:customer",
        "edge_type": "claim_to_object"
      }
    ]
  }
}
```

Business Graph 只消费已进入 BKN Trace 的 `business_refs`，并复用当前用户身份调用 BKN / Vega resolver 做实时授权投影。`unresolved` 与 `unauthorized` 不会生成业务节点或边；resolver 不可用会明确标记披露降级。没有安全实例级授权接口的对象实例、行动实例和函数引用保持 fail-closed，不能仅凭父类型权限披露。

Evidence Node 查询用于打开单个可见节点详情：

```http
GET /api/agent-observability/v1/business-provenance/evidence-nodes/claim%3Aclaim_handler?trace_id=9c0d...
GET /api/agent-observability/v1/business-provenance/evidence-nodes/business_ref%3Aobject%3Acustomer?request_id=req_handler_002
```

首版 node id 格式：

```text
claim:{claim_id}
evidence_ref:{ref_id}
business_ref:{ref_id}
```

查询必须提供且只能提供一个 scope：`trace_id` 或 `request_id`。当前阶段只返回 `visibility=visible` 的节点；`hidden`、`redacted`、`omitted`、`unresolved`、`unauthorized` 节点不会通过详情接口展开。

Evidence ingestion 默认写入 `2.1.0`，读取兼容 `2.0.0`。2.1 事件执行精确 payload/ref allowlist、类型、枚举、hash、敏感值、trace/span/time join 校验。一般事实允许先于其父事件异步到达：首次查询返回 `causality_missing`，父事件补到后自动恢复完整；claim 的 `source_event_ids` 缺失仍返回 `source_event_missing`，Action 前态乱序继续原子拒绝。混合 2.0/2.1 历史按每个 event 的版本保留 `causality_missing`。

OpenSearch 当前仍使用“单 trace 聚合文档 + OCC”保障 Action 状态和事件冲突原子性。为避免文档无限增长，单 trace 硬限制为 10,000 个事件且聚合 JSON 不超过 8 MiB，超限返回 `BKN_TRACE_CAPACITY_EXCEEDED`。该限制不是最终扩展方案；后续需要迁移为事件文档加状态投影，并使用 PIT 分页。旧索引中缺少 tenant/account 必要归属的 2.0 聚合文档默认不可查询、不可继续追加；`2.0` 兼容仅指已有归属事件的语义读取。缺失归属必须通过离线受控迁移补齐，不能由请求方认领。

持久化 evidence 可通过企业业务溯源接口回查：

```text
GET /api/agent-observability/v1/business-provenance/traces/{trace_id}/evidence-chain
GET /api/agent-observability/v1/business-provenance/requests/{request_id}/evidence-chain
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
http://localhost:8080/api/agent-observability/v1/swagger/index.html
http://localhost:8080/api/agent-observability/v1/swagger/doc.json
```

统一发布的 OpenAPI YAML 位于 `../../docs/api/agent-observability/agent-observability.yaml`。

## Docker

本地构建镜像：

```bash
make docker-build
```

默认镜像名：

```text
swr.cn-east-3.myhuaweicloud.com/openbkn-ai/agent-observability:local
```

也可以覆盖：

```bash
make docker-build IMAGE=swr.cn-east-3.myhuaweicloud.com/openbkn-ai/agent-observability:v0.1.1
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
  --set image.repository=swr.cn-east-3.myhuaweicloud.com/openbkn-ai/agent-observability \
  --set image.tag=0.1.1 \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  --set opensearch.auth.enabled=false \
  -n observability --create-namespace
```

启用 OpenSearch Basic Auth：

```bash
printf '%s' 'your-username' > /path/to/opensearch-username
printf '%s' 'your-password' > /path/to/opensearch-password

kubectl create secret generic bkn-trace-opensearch \
  -n observability \
  --from-file=username=/path/to/opensearch-username \
  --from-file=password=/path/to/opensearch-password

helm upgrade --install agent-observability charts/agent-observability \
  --set image.repository=swr.cn-east-3.myhuaweicloud.com/openbkn-ai/agent-observability \
  --set image.tag=0.1.1 \
  --set opensearch.endpoint=http://opensearch-cluster-master:9200 \
  --set opensearch.auth.enabled=true \
  --set opensearch.auth.existingSecret=bkn-trace-opensearch \
  -n observability --create-namespace
```

不要通过 Helm `--set` 传递 OpenSearch 用户名或密码。Chart 只接受已有 Secret 的名称；用户名与密码键默认为 `username`、`password`，可用 `opensearch.auth.usernameKey` 与 `passwordKey` 覆盖。
创建凭据文件时使用 `printf`，避免 `echo` 在文件末尾写入换行。

## 历史 clean-slate 数据清理（非 0.1.3 → 0.1.4 升级入口）

`scripts/cleanup_legacy_bkn_trace_data.sh` 是早期 clean-slate 切换后退役旧目标的历史工具；它**不是** 0.1.3 → 0.1.4 的生产升级入口。该升级必须使用 [BKN Trace 0.1.3 升级到 0.1.4](../../help/zh/manual/bkn-trace-0.1.3-to-0.1.4-upgrade.md) 中的 `deploy/scripts/upgrades/0.1.4/cleanup_legacy_bkn_trace_data.sh`，后者包含停写观察、集群上下文和依赖目标校验。

历史工具仅用于在 0.1.4 clean-slate 切换验收通过后，退役明确指定的旧 BKN Trace 服务库表及 Trace、Evidence、Projection 索引中的文档；不会清理日志索引或 MCP、SDK、BKN、Vega 业务数据，也不会自动运行。脚本默认只预览数量；核对目标并完成 Owner 确认后，才可显式增加 `--confirm`。MariaDB 密码可通过 `MYSQL_PWD` 提供，OpenSearch Basic Auth 可通过 `OPENSEARCH_USERNAME` 与 `OPENSEARCH_PASSWORD` 成对提供。

必须显式设置：`BKN_TRACE_CLEANUP_DB_HOST`、`BKN_TRACE_CLEANUP_DB_NAME`、`BKN_TRACE_CLEANUP_DB_USER`、`OPENSEARCH_ENDPOINT`、`OPENSEARCH_TRACE_INDEX`、`OPENSEARCH_EVIDENCE_INDEX`、`BKN_TRACE_PROJECTION_INDEX`。脚本拒绝空值、未展开表达式、通配符、系统库及系统索引，并在清理后回读确认所有指定目标均为零。升级前版本尚不存在的明确允许表会报告 `status=absent` 并跳过，不会扩大清理范围。

完整的目标隔离、切换验收、退役与共享日志处置流程见 [Trace 0.1.4 clean-slate 切换与 0.1.3 数据退役](docs/0.1.4-clean-slate-cutover.md)。

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
swr.cn-east-3.myhuaweicloud.com/openbkn-ai/agent-observability
```

需要配置的 GitHub Secrets：
- `SWR_USERNAME`
- `SWR_PASSWORD`

Chart 会推送到：

```text
ghcr.io/<github-owner>/charts
```
# test trigger
