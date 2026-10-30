# agent-observability

基于参考项目 `agent-factory` 的六边形架构实现的 Agent Trace 查询服务。

当前提供：
- 类型化 Trace 列表接口：`GET /api/agent-observability/v1/traces`
- 单 Trace 技术详情接口：`GET /api/agent-observability/v1/traces/{trace_id}`
- Evidence 事件接收接口：`POST /api/agent-observability/v1/evidence/events`
- 受管 Interaction 读取：`GET /api/agent-observability/v1/interactions/{interaction_id}`
- 已记录 Operations 读取：`GET /api/agent-observability/v1/interactions/{interaction_id}/operations`
- Core 业务语义组装修订：`GET /api/agent-observability/v1/interactions/{interaction_id}/business-graph`
- OpenSearch 查询客户端
- 阶段二 Evidence ingestion 校验、归一化和可替换存储接口，支持内存 store 与 OpenSearch evidence index store
- Swagger 文档生成
- Docker 镜像构建
- Helm Deployment Chart
- GitHub Actions 构建与发布流水线

## Community 与 EE 读取边界（#2517）

以下路径均以 `/api/agent-observability/v1` 为前缀，描述当前主线实现。
Community 0.2.0（`81d57528`）已有同样的企业路由装配边界；升级 Community
镜像不会自动获得 EE 原生业务溯源接口。公开 Core OpenAPI 不包含 EE 路由。

| 读取入口 | 制品与含义 |
| --- | --- |
| `GET /interactions/{interaction_id}` | Core：受管 Interaction 生命周期及已记录事实 |
| `GET /interactions/{interaction_id}/operations` | Core：已记录调用的输入、输出/错误等事实 |
| `GET /interactions/{interaction_id}/business-graph` | Core：已记录业务语义组装修订的授权投影；不是 EE 原生解析详情或解释结果 |
| `GET /traces`、`GET /traces/{trace_id}` | Core：技术执行列表与详情，继续受当前读取授权和展示范围约束 |
| `GET /business-provenance/conversations`、`GET /business-provenance/interactions` | EE：业务会话/交互检索，Community 不挂载 |
| `GET /business-provenance/interactions/{interaction_id}` | EE：当前交互的企业业务解析详情，Community 不挂载 |
| `GET /business-provenance/interactions/{interaction_id}/explanations` | EE：只读查询当前授权作用域的解释；不存在已生成结果时为 `not_generated`，存在时为 `ready` |

EE 实例必须实际包含并装配企业实现，通过运行时企业许可门，再经过 Core 的当前身份
与记录范围授权。安装其他 EE 服务或拥有企业证书，不会让 Community Trace 二进制
装配不存在的实现。管理员角色也不能替代许可或记录授权。

### 如何解释 404

- Community 的企业路由没有注册，普通 `404 page not found` 符合实现边界，不说明
  Interaction 丢失，也不是单凭这一响应就能判定的网关故障。
- 当前 EE 许可门未通过时也刻意返回相同的普通 404；企业详情中的未披露记录也可能
  返回 404。因此状态、Content-Type 或响应体不能独立区分缺路由、未许可和未披露。
- 标准 Core 读取在缺少有效身份时返回 401；`resource_not_disclosed` 是该读取合同的
  结构化 404，表示请求的记录在当前授权范围中不可披露，不能证明记录不存在。
- 标准 Interaction/Operations 返回 200，仅证明这些基础事实可读，不证明 EE 原生详情
  或解释已安装、获许可或获得授权。排查时分别核对目标 Trace 镜像/装配、运行时许可和
  当前记录授权，不根据一次 404 自动选择“缺许可证”等单一原因。

解释 GET 不触发生成。只有显式生成动作才调用独立的 EE
`POST /business-provenance/interactions/{interaction_id}/explanations`，仍需当前许可与授权；
打开面板或探测可用性不能代替用户发起生成。`completed`、Operations 非空或 Core 图可读，
都不意味着已有 `ready` 解释，也不代表解释或事实完整。

外部客户端可继续显示已授权的 Core/本地事实，将企业原生视图标为不可用或未披露；
不能把基础图转换成伪造的 EE 节点、关系或解释。企业功能的非空验收应使用获授权 EE
环境中已有的真实记录，分别保留详情和解释读取结果。没有现成解释时保留 `not_generated`，
不为验收制造生成请求。本说明不把其他部署的成功结果反向记为原 Community 环境通过。

旧的 `/business-provenance/traces/.../evidence-chain`、
`/business-provenance/requests/.../business-graph` 和
`/business-provenance/evidence-nodes/...` 等 trace/request/node 读取示例不属于当前挂载合同，
不能将内部 Evidence Store 模型或旧响应 envelope 当成当前可调用的 HTTP 接口。

## Span display and browsing (#2158)

Technical detail keeps its existing single graph read: at most 1,001 raw records,
then at most 1,000 unique Span nodes. `span_display` reports loaded nodes, raw
records, the display limit and loaded start-time range. `total_count` reuses the
existing authorized summary cardinality estimate (`total_accuracy=estimated`);
unavailable or stale estimates are `null`/`unknown`. Truncation means a bounded
display window, not proof of collection loss. Duplicate raw records can consume
the window before 1,000 unique nodes. A missing parent in that window is
inconclusive: it may be uncollected or not loaded. The `parent_outside_display`
compatibility reason does not prove that a parent record exists outside the
window. The graph query orders by start time then Span ID.

`GET /api/agent-observability/v1/traces/{trace_id}/spans?cursor=...` is a separate,
on-demand browser. Each response replaces the current page with up to 100 unique
Span summaries ordered by Span ID. It never expands the graph. Composite
aggregation groups duplicate stored Span IDs and returns one recorded version;
conflicting duplicate versions have no preferred revision. The endpoint supports
current SS4O flat records and individual OTLP Span records with aggregatable
`spanId.keyword`; legacy whole `resourceSpans` envelopes are explicitly unsupported
(422), including mixed storage with missing top-level Span IDs.

Only active `admin`/`super_admin` profiles may read raw Trace spans. Every page
also rechecks canonical Trace visibility. Other authorized users retain scoped
summary/Operations detail, with `span_scope_unavailable` and no global Span count
or raw graph. Cursors are signed, expire after 15 minutes and bind the Trace,
identity, current grants, sort and fixed page size. Grant sets are canonicalized;
permission ordering and display-name changes do not invalidate a cursor, while
actual identity/grant changes still do. Replicas reuse the existing
cursor signing key; without one, process-local cursors expire on restart or
routing to another process. A request has a 3-second budget and a 4 MiB upstream
response limit; partial, timed-out or oversized searches fail explicitly.

Pagination is live, not a snapshot. Records inserted before the current Span ID
may require restarting from the first page. A full final page may be followed by
an empty terminal page. No first-screen count request, migration or index mapping
change is introduced. Studio adds the browser through its coordinated PR; older
Studio versions ignore the additive metadata.

## Bounded Trace archive batches (#1724)

One archive request automatically processes successive Trace batches using the
existing batch jobs and progress fields. Each selection reads at most 100
Interaction IDs, closes that result, and packages one Interaction at a time.
The complete JSON payload of one Interaction is limited to 8 MiB; a batch's
JSONL data, including newlines, is limited to 10 MiB. The batch leaves headroom for the
base64-encoded recovery payload and metadata stored in one SQL statement under
MariaDB's default 16 MiB `max_allowed_packet`; no server setting is changed. Byte-limited batches leave
the next Interaction for the following selection after verified cleanup.

Before reading SQL payload collections, an archive-only repeatable-read snapshot
checks their variable-length bytes and row counts (at most 8 MiB and 32,768 rows).
The final serialized payload is checked again. This bounds loading before JSON
assembly; it is not a process RSS target. Existing opt-in revision-input locking
and validation are retained. Technical Trace enrichment runs one Interaction at
a time, with a bounded OpenSearch response and a final payload limit. Ordinary
list/detail reads and the existing Span-browser response budget are unchanged.

Every batch has its own verified object, frozen candidates and persisted progress.
Only successful cleanup advances the run. Cleanup failure stops the request;
retry revalidates that batch's existing archive before cleanup and continuation.
The cutoff remains fixed across the request and resumed batches. Totals and
remaining counts describe each selection, not a permanent snapshot of all
candidates at request start. Large eligible counts alone no longer load all Trace
payloads into memory. The object writer also checks the Trace batch budget before
allocation/upload and bounds its verification download to the frozen length.

A single oversized Interaction or unverifiable technical response stops before
writing/deleting that batch, returning HTTP 409 `archive_incomplete_candidates`
with a payload-budget explanation where applicable. Earlier completed batches
remain recorded. Oversized records require operator handling; retries do not
truncate, skip or delete them. The existing manifest and database formats are
unchanged; no migration or streaming object protocol is introduced. SQL cleanup
also verifies frozen source metadata under its parent row lock, as described below.

## Technical Trace archive cleanup safety (#1724)

Technical Trace enrichment requires a complete OpenSearch result: no timeout,
failed/missing shard evidence, lower-bound total, truncated window, malformed
source or missing document identity/version. The existing 10,000-document read
window remains; an incomplete selection stops before object verification or
cleanup.

New packages retain the existing `technical_traces` sources and additionally
freeze each record's concrete index, document ID, sequence number, primary term
and optional routing in `technical_trace_records`. This metadata travels with
the existing candidate payload and cleanup retry record; no migration is added.
Only those document versions are conditionally deleted after object verification.
Late records with the same Trace ID remain hot. Changed versions are retained and
leave the job `cleanup_incomplete`; retries do not reselect or widen the range.
Already-removed records are accepted only when their absence is confirmed.

Legacy packages with technical Trace references but no frozen document metadata
remain readable/downloadable, but automatic cleanup is refused. Their existing
job and frozen payload stay available for operator review. No fallback deletes
by Trace ID. Packages with no technical Trace references retain their no-op
technical cleanup behavior.

Before retrying cleanup, the service re-reads the existing manifest and JSONL
object through the object-store read port. The manifest must match the durable
job ID, kind and candidate count, reference either its sibling `data.jsonl` or the v0.1.4 writer layout
`<prefix>/<tenant>/<jobID>.jsonl` alongside `<jobID>/manifest.json`, and carry
the SHA256 of the frozen candidate payloads in their original order. A streamed
read verifies the exact expected byte length and digest without loading another
full bundle. Missing, unreadable or mismatched objects, or a custom object store
without read support, refuse cleanup. The job remains `cleanup_incomplete` with
its frozen candidates and progress intact. Safe task hints distinguish storage
read failures (check access and retry), mismatched contents (restore the matching
archive), and missing recovery records/read support (operator review).
The existing manifest format is unchanged, and retry never rewrites the archive.
This checks the objects read during retry; it does not lock object-store versions
against later external changes.

Archive credentials must permit archive object reads as well as bulk document
deletion and real-time document reads on the concrete indices. A denied/ambiguous
response preserves the incomplete-cleanup state.

Before hot SQL cleanup, the existing Interaction `FOR UPDATE` read retrieves its
current `record_integrity_version` and bounded `record_integrity_json`. Both must
match the frozen package: late receipt/fact/lifecycle writes change the source
version, while a newly computed integrity verdict can arrive at the same version.
The verdict comparison covers the full JSON object, ignores key order/whitespace
and preserves exact numbers. New archive packages capture the original bounded
integrity JSON from the same Interaction row/snapshot, including fields unknown
to the current binary. Technical enrichment preserves that JSON and its numeric
precision. Older packages that already lost extension fields cannot prove an
identical complete verdict and still require operator reconciliation.
Parent row locks remain held through deletion and
commit, and one changed candidate rolls back the whole SQL batch, including
previous candidates. Already removed rows remain idempotent; existing sealed
revision-input checks remain in force.

Frozen packages need an explicit `record_integrity_version` (zero is valid) and
matching Interaction identity. Missing or unverifiable legacy metadata refuses
SQL cleanup. The task stays `cleanup_incomplete` with its original frozen
payloads, manifest and progress for operator review. A changed source is not
silently refrozen or replaced during retry. That refusal blocks new same-kind
archives; repeating the same retry cannot reconcile a genuine source change.
This is a SQL transaction guarantee,
not an atomic transaction across OpenSearch and MariaDB: conditional deletion of
already archived frozen technical documents may have finished before SQL refuses
cleanup, while late or changed technical versions retain their existing guards.
The bounded Trace batches above limit package loading without a streaming
redesign. List/detail query paths are unchanged.

## Error-keyword query cost and storage failures (#2675)

Trace `error_keyword` searches in global scopes screen lightweight receipt
identities against the existing first-failure metadata before loading complete
Trace receipts, related Interactions and artifacts. Screening reuses the same
recorded error-summary formatter as final matching: ordinary errors keep the
first 512 Unicode code points and wrapped execution stderr keeps the last 512.
It does not search arbitrary input/output or discarded error text. All matching
candidates remain reachable; existing scan, time and result-retention budgets
still apply. No index field, schema migration or historical backfill is needed.

Candidate screening never authorizes a result. Complete receipt/Interaction
authorization and final summary matching still run for every possible match.
Owner and shared-network scopes retain complete candidate discovery and Interaction
expansion, because an authorized sibling, including a mixed-owner historical receipt, may not appear in the initial receipt
scope. Older stores without narrow first-failure metadata also retain the
complete scanner. These paths retain their existing read cost.

PIT requests preserve rejected HTTP status and a bounded exception type while
discarding storage reasons, query text and document content. OpenSearch 429 and
5xx errors return `503 SUMMARY_DEPENDENCY_UNAVAILABLE`; the caller may retry
later. The server performs no additional automatic retries. Diagnostic warnings
include the response Trace ID, dependency status and safe exception type; read
requests do not produce business audit events or additional business Traces.

## Root-service discovery (#2140)

`GET /api/agent-observability/v1/trace-root-services` returns bounded canonical
root-service names under the current trusted Trace identity. It accepts `from`,
`to`, `trace_id`, `status`, `tool`, `error_keyword`, a case-insensitive substring
`q` (at most 256 Unicode characters), and `limit` (1–100, default 100). The current
`service` filter is excluded from discovering its own alternatives; list paging
and unsupported filters are rejected rather than silently dropped.

The response is `{ "entries": ["service-name"], "has_more": false }`. Names keep
their original spelling for the existing service filter. `has_more` means more
names match than fit in this selection; refine `q` to find them. This is not a
paginated global service inventory and does not provide approximate Trace counts.

Simple global queries use the same hot SQL receipt membership as ordinary
Trace lists. Status/error, shared-scope and exact-Trace queries reuse the bounded
authorized receipt scanner. Root-service SQL metadata and service filtering now
select source facts through authorized receipt tuples, so an earlier foreign
fact with the same Trace ID cannot replace an owner's root service. Global
reader semantics are preserved. No schema or new materialized model is added.

Discovery has a 3-second query budget and returns no names on an actual failure,
truncated input or timeout. Once an extra distinct authorized name proves
`has_more`, the scanner stops loading remaining metadata batches while retaining
PIT cleanup. The SQL discovery statement additionally uses MariaDB's per-statement
`max_statement_time=3`; it does not change session/global limits. Server timeout
1969 is mapped to the existing query-timeout response. MariaDB checks its timeout
at execution checkpoints, so this is a server-side budget rather than an exact
wall-clock bound. The production artifact reader skips question/result
content and all Span statistics; error filtering still reads the necessary
recorded failure metadata. Query cost can grow with the matching receipt set, so
budget errors ask users to narrow the time range/filters. Existing list/detail
content loading remains unchanged by default.

Studio loads candidates only after explicit opening, preserves manual exact
input, and cancels stale requests when other draft filters change. Selection
changes the draft; the existing Query action submits it. Older backends or failed
discovery retain manual input. Initial page/detail request counts do not change.

## Development

### 系统审计配置

`kafkaConsumers.audit.enabled` 默认关闭。此时 `/api/observability/v1/log-sources`
仍返回 `audit-ledger`，状态为 `not_integrated`，原因为
`audit_consumer_not_configured`，并提供 `required_action`。
单独查询 `audit.admin` / `audit.security` 返回 HTTP 503，错误码相同，
`retryable=false`；完成配置前反复刷新不会恢复。混合查询保留可用记录，
同时返回审计配置状态并将结果标记为 partial。

迁出前的 opt-in profile 与部署后只读事件验收说明见 [System Audit profile 历史版本](https://github.com/openbkn-ai/bkn-foundry/blob/191928e1cda4667ec23c75d7e4aba637e513207f/deploy/conf/profiles/README.md)。独立部署仓库尚未包含这份 profile；启用前须确认安装器已支持相应配置。需要系统审计的部署，在安装器使用的配置文件中显式加入：

```yaml
core:
  store: mariadb
  autoMigrate: true
  mariadb:
    existingSecret: bkn-trace-core-mariadb
    dsnKey: dsn
kafkaConsumers:
  audit:
    enabled: true
    brokers: ["<kafka-host>:<port>"]
    topic: openbkn.audit.v1
    consumerGroup: bkn-trace-audit-ledger-v1
    saslMechanism: PLAIN
    existingSecret:
      name: <audit-kafka-credentials-secret>
      usernameKey: username
      passwordKey: password
```

先准备配置引用的 Secret、MariaDB 数据库与 `openbkn.audit.v1` topic，
再执行正常安装/升级。相同 Chart 版本的配置更新需要传 `--force-upgrade`。
安装器通过 Helm dry-run 校验 consumer 配置，再检查凭据与 MariaDB DSN
Secret 的 key 是否存在且非空；关闭 consumer 时不会读取它的凭据。
Kafka 认证、topic 可访问性和网络故障仍由现有 consumer 运行时处理；
此 preflight 不表示 Kafka 已连通。

升级时仅保留已记录的 Audit consumer 配置，当前配置文件和显式 `--set`
优先；配置文件中写 `enabled: false` 可关闭它。使用独立 Chart 升级时，
仍需在 values 文件中保存并传入上述配置。通过临时修改 Deployment 环境变量
开启 consumer 不会保存配置。

部署后使用具有 `audit` 或 `super_admin` 角色的账号检查源状态，再发送一条真实审计事件，确认其入账且可由
`GET /api/observability/v1/logs?categories=audit.admin` 查询。
HTTP 200 空列表只能证明查询源可用，不能证明 Kafka 采集已经完成。


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

历史 Evidence Store 模型包含 `trace`、`events`、Claim、evidence ref 和 business ref，并保留内存与 OpenSearch 存储实现。内部存储模型不决定当前公开路由；受管证据写入采用下文 BKN Trace 3.0 合同，读取范围见前述 Community/EE 边界。

### BKN Trace 3.0 Interaction 业务语义图

`POST /api/agent-observability/v1/evidence/events` 与 `GET /api/agent-observability/v1/interactions/{interaction_id}/business-graph` 使用 BKN Trace 3.0 强类型合同。事件必须使用 `bkn.trace.schema.version`，归属只能来自可信身份上下文，请求体中的旧 `schema_version` 或调用方自报主体字段会被拒绝。

Interaction 业务语义图遵循以下口径：

- `adopted_supports`、`rejected_supports` 和 `unused_evidence_refs` 互斥；Claim 级 unused 表示既未被该 Claim 采纳、也未被该 Claim 拒绝，全局 unused 表示未被本修订任何 Claim 分类。
- `claim_status=withdrawn` 保留历史 Claim 及其支持关系，但不参与当前修订的证据完备性判断。
- 相同支持合同命中多个版本、时间点或证据类型时，不任意选择，返回 `support_target_ambiguous` 并标记客观证据不完整。
- `completeness` / `partial_reasons` 描述客观证据组装；`disclosure_partial` / `disclosure_reasons` 描述当前用户授权投影。resolver 不可用、未配置或无法确认权限时，业务节点及其操作边默认不披露。
- resolver 按 `ref_type + ref_id + source_system` 判定，不使用不匹配的 RefID 前缀推断权限。暂时没有安全实例级授权接口的类型保持 `unresolved`，不能由父类型权限推断实例权限。

失败回执保留生产端明确给出的失败分类：本轮修改的 MCP 查询/模式 handler（`search_instance`、`query_object_instance`、`query_instance_subgraph`、`explore_subgraph`、`query_metric`、`get_kn_detail`、`get_object_types`、`get_relation_types`、`run_sql`、`run_cypher`）中的显式参数绑定、必填值或格式校验拒绝，以及本地搜索、指标、SQL / Cypher 校验，写入顶层 `error.stage=input_validation`。REST 受管调用同样保存执行服务记录的分类，本轮实际覆盖 SQL 校验和后端失败；未将全部 REST handler 的绑定错误纳入此覆盖范围。未修改的 schema search、logic properties、action、network/resource discovery 等 MCP 工具采用 `execution_only` 合同，其 RequiredTraceFields 不要求 `business_refs`；不将本轮结果声明为所有 MCP 校验路径均已适配。MCP 的 `IsError`、REST 的 HTTP 状态和原始错误内容保持不变；不会从 HTTP 状态、错误文本或调用者提供的 `structuredContent` 推断阶段。

`run_sql` 将执行层已有解析器识别的资源 ID 同时保留到受管调用的 `business_refs`，供失败回执归因。这个事实独立于 Kafka 入队或 Ledger 持久化：事件被丢弃仍保留原来的失败持久性状态。目标随既有 evidence expectation 在第一次 Finish 前冻结，迟到回调不能改写重放的目标。`sql_guard` 没有资源目标时仍是策略拒绝；非输入拒绝且缺少回执引用和匹配事件，Core 继续返回 `failed-call target context unavailable; no explicit input rejection`，历史回执不补造事实。

可用下列命令验证真实 MCP handler → Finish / publisher 原始消息 → Kafka consumer → Ledger 服务 → 完整性判定。默认使用合成输入和 memory Ledger store，验证原始消息头、准入、所有者解析与重复消费。设置 `BKN_TRACE_TEST_MARIADB_DSN` 后，每个案例创建并清理独立数据库，通过真实生命周期服务保存和重放失败请求，核对事件到达前后的数据库快照及 Ledger / projection outbox 单行幂等性；该测试 DSN 必须允许创建和删除临时数据库。

```bash
bash bkn-trace/agent-observability/scripts/test_failed_call_contract.sh
```

同时设置 `BKN_TRACE_TEST_KAFKA_BROKERS`（单个 broker 地址）后，原始消息经真实 Kafka 两次投递，再由生产 `KafkaProcessor` 写入 MariaDB。broker 必须是隔离测试实例，预先创建单分区 `openbkn.evidence.v1` topic 并配置 `message.timestamp.type=LogAppendTime`。此契约测试不验证 consumer group offset 提交。

设置 `BKN_TRACE_TEST_OPENSEARCH_ENDPOINT` 后，同一测试通过真实 projection outbox worker 消费 MariaDB 记录，对比事件到达前后 receipt / evidence_event 文档与数据库快照，并确认已交付记录不再被租赁。每个案例使用独立索引并清理；若需要认证，设置 `BKN_TRACE_TEST_OPENSEARCH_USERNAME` 和 `BKN_TRACE_TEST_OPENSEARCH_PASSWORD`。

部署验收使用实际 MCP 生命周期入口和 REST SQL 入口。主线已通过 #1841 将 `run_sql` 从所有 MCP profiles 退役，直接 handler 契约测试不代表该工具仍对外发布。REST 当前不附加 MCP capability profile，因此其 registered call records 完整性不能替代 MCP 合同中的严格目标判定；无目标 `sql_guard` 的严格 Core 负例由前述契约测试验证，实际 REST 业务溯源保留 `unresolved/resource_id`。

在独立测试部署上运行（脚本会创建五组测试会话，不应指向生产环境）：

```bash
# Token 从测试环境提供，勿写入仓库或报告。
python3 bkn-trace/agent-observability/scripts/test_failed_call_deployment.py \
  --mcp-url http://127.0.0.1:23376/api/agent-retrieval/v1/mcp/ \
  --retrieval-url http://127.0.0.1:23376 \
  --core-url http://127.0.0.1:23375 \
  --opensearch-url http://127.0.0.1:23374 \
  --projection-index issue2072-core --report /tmp/2072-deployment-report.json
```

环境需提供 `BKN_TRACE_TEST_DEPLOYMENT_TOKEN`、业务溯源路由装配、artifact writer、真实 Kafka consumer 和 projection worker；Vega 测试依赖应对 inventory 查询返回失败。脚本核对输入、终态错误阶段、资源引用、持久回执与实际消费事件的 OpenSearch 投影，并验证业务溯源查询。仅 receipt 投影成功不会被视为 Kafka 事件消费成功。

本地验收使用 Core/检索实际二进制、真实 Kafka/MariaDB/OpenSearch，认证、Vega 故障和 Collector 队列指标由固定测试依赖提供；业务溯源使用本地 EE `ee_dev` 装配。这是开发部署验收，不包括 Helm 升级、生产许可证或真实上游系统验收。结果与边界见 [#2072 本地验证记录](../../docs/validation/2026-10-09-2072-failed-call-deployment.json)。

`ref_type` 与 `ref_id` 的规范结构是一一对应的写入合同。事件与 operation receipt 两条写入路径都必须使用完整限定格式；前缀、段数或版本不合法时会拒绝写入，避免业务节点在查询时静默消失。

| `ref_type` | `ref_id` 规范结构 |
|---|---|
| `knowledge_network` | `kn:<kn_id>` |
| `object_type` | `object:<kn_id>:<object_type_id>` |
| `object_instance` | `object_instance:<kn_id>:<object_type_id>:<opaque_instance_id>`（不透明尾段可包含 `:`） |
| `property` | `property:<kn_id>:<object_type_id>:<property_id>` |
| `relation_type` | `relation:<kn_id>:<relation_type_id>` |
| `data_resource` | `resource:<resource_id>` |
| `metric` | `metric:<kn_id>:<metric_id>:v<version>`（历史三段引用仍可读取，但不会再向旧接口解析） |
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

Evidence Store 的内部查询预算不构成公开 HTTP 合同。当前读取入口及 Core/EE
差异见[读取边界](#community-与-ee-读取边界2517)，不要向未挂载的旧 trace/request
企业路径发送查询或沿用其 `limit`、`page`、`partial_reason` 响应假设。

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

### Trace/Evidence admission budget configuration

Configure `observability.admissionBudget` in the persistent values file used for every Helm install and upgrade. The four thresholds are deployment/SLO inputs, must be finite numbers in `(0, 1]`, and have no implicit defaults. The required keys are `opensearchCapacityThreshold`, `opensearchHeapThreshold`, `collectorQueueThreshold`, and `storagePoolThreshold`.

The Foundry `deploy.sh config generate` path preserves the existing top-level `observability` section, including thresholds, dedicated/fallback Collector endpoints and explicit disablement. Regenerating middleware configuration does not select thresholds or replace these reviewed deployment choices. Keep this file as the input to subsequent upgrades; an environment-only Pod patch remains temporary. When starting with a new file, supply the reviewed values explicitly: missing budget inputs remain unavailable with structured diagnostics rather than receiving inferred defaults.

Set `collectorMetricsEndpoint` to the Collector's complete Prometheus URL, including the metrics path (the bundled Collector exposes `/metrics` on port 8888). This dedicated endpoint takes precedence. If it is empty, the service retains the `BKN_OBSERVABILITY_SOURCE_COVERAGE_METRICS_ENDPOINT` fallback; the Chart injects that fallback only when `observability.sourceCoverage.enabled` is true. An invalid dedicated endpoint is reported rather than silently replaced by the fallback.

The endpoint must return both `otelcol_exporter_queue_size` and `otelcol_exporter_queue_capacity`, with a nonnegative size and positive capacity. A zero size is valid; a missing size is unavailable. The remaining measurements come from OpenSearch filesystem/JVM statistics and the configured MariaDB connection pool.

Configuration errors are reported at startup. Configuration GET and enable requests retain `503 POLICY_RECONCILER_UNAVAILABLE` when the budget is unavailable, with optional `details.reason`, `details.metric`, and `details.fields` identifying the failure. Reasons are `invalid_configuration`, `source_unavailable`, `metric_missing`, or `metric_invalid`. Full dependency errors are available at Debug level and associated with the response `trace_id`. Set `BKN_TRACE_LOG_LEVEL=debug` (Helm value `logLevel: debug`) and restart the service to enable them; the default is `info`, and `warn`/`error` are also supported. Invalid levels fail startup with the configuration field name; repeated polling failures do not emit WARN logs. Strict queue validation applies only to admission reads, so invalid queue samples do not prevent source-coverage monitoring from reading refused/failed counters. A valid budget over a threshold remains readable through GET, while an enable request returns `422 ADMISSION_BUDGET_EXCEEDED`. Disabling does not require a budget. Budget availability does not change the service's liveness or readiness probes.

### Evidence 写入安全边界

受管 Conversation、Interaction、Operation 生命周期继续监听于集群内部的 `agent-observability-internal:8081`，同时在公开 8080 接口为 OAuth SDK 客户端开放。公开写入由服务端根据 OAuth 与 BKN Safe 身份派生 owner，不接受客户端伪造 owner。Evidence Event 经 Kafka 进入 Ledger；Artifact 继续使用独立的 8080 HTTP 接口和凭据。公开读取仍由 OAuth 与 Access Profile 保护。Chart 的 NetworkPolicy 默认允许带 `app.kubernetes.io/name=agent-retrieval` 或现有 `app=agent-retrieval`、`app=agent-operator-integration`、`app=bkn-agent` 标签的 Pod 访问 8081；默认还允许 `openbkn` namespace 的 `module=bkn-backend`、`module=ontology-query` 及本 namespace 的 `app.kubernetes.io/name=otelcol-contrib`；其他布局通过 `networkPolicy.allowedClients` 显式配置。

When upgrading to this internal control protocol, do not use `helm upgrade --reuse-values` with the previous Chart's default `networkPolicy.allowedClients`: it retains the old list and omits Backend, ontology-query, and Collector. Use `helm upgrade --install ... --reset-values -f <reviewed-custom-values.yaml>` so the new Chart defaults apply, and reapply the deployment's explicit custom settings. If the values file defines `networkPolicy.allowedClients`, update that list to include every actual internal caller; the list replaces the defaults and no implicit workload rules are added. The repository installer passes an explicit configuration file and does not use `--reuse-values`. Verify access to port 8081 from each workload after upgrading.

该 NetworkPolicy 依赖 Kubernetes 1.23 或更高版本。离线执行 `helm template` 时应显式传入 `--kube-version 1.23.0` 或实际目标集群版本；连接集群的 `helm install/upgrade` 会按目标集群能力校验。

Chart 默认不创建或接管 `bkn-trace-evidence-ingest` Secret。OpenBKN 整体安装器在 release 循环前创建或验证该 Secret，并将同一 token 注入 Agent Observability、Context Loader、Vega、BKN Backend、Ontology Query、BKN Agent 与行动执行服务；BKN Backend 和 Ontology Query 同时启用其持久 Evidence outbox worker 与既有清理任务（已投递记录保留 30 天，放弃记录保留 180 天）。单独安装任一 Chart 时，应预先创建并显式引用该 Secret。对无 outbox 的生产者，禁用 Evidence 写入须同时清空 ingest URL 与 token Secret；对启用了 producer outbox 的 BKN Backend 和 Ontology Query，须同时关闭 outbox 与 worker，不能只清空 URL。`evidence.ingestAuth.createSecret=true` 只适用于 Helm 直接执行的首次安装，不适用于 `helm template | kubectl apply`，也不能用于接管已有的外部 Secret。

Trace/Evidence 控制面通过 `agent-observability-internal:8081` 提供无鉴权、无签名的内部接口，不通过公开 Service 或 Ingress 暴露：

- `GET /api/agent-observability/v1/internal/trace-evidence/policy`：返回 `TraceEvidencePolicySnapshotV1`，仅包含版本、revision、Trace/Evidence 开关和签发/过期时间。
- `GET /api/agent-observability/v1/internal/trace-evidence/configuration`：读取当前配置和操作，用于确认状态切换。
- `POST /api/agent-observability/v1/internal/trace-evidence/endpoints:heartbeat`：请求包含 `endpoint_kind`（`trace_gateway` 或 `evidence_publisher`）、`workload_identity`、`instance_id`、`process_boot_id`、`observed_revision`、`ready`；`instance_id` 必须等于 `workload_identity#process_boot_id`。
- `POST /api/agent-observability/v1/internal/trace-evidence/operations/{id}:ack`：Gateway 使用请求体中的 workload/process 标识确认操作。
- `POST /api/agent-observability/v1/internal/trace-evidence/operations/{id}:publisher-ack`：Publisher 使用 `producer_instance_id` 的 `workload_identity#process_boot_id` 标识确认操作。

身份字段只用于内部进程状态关联，不进行服务身份认证。保留 revision、快照有效期、操作阶段和队列闭合校验。Chart 不再要求策略签名 Secret，快照有效期由 `core.capturePolicySnapshotTTL`（默认 `15m`）配置。公开用户接口仍保留 OAuth / Access Profile 鉴权。

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

Context Loader 通过内部监听器携带 application principal、effective subject type/id 和可选 delegation 构成的 owner tuple。公开监听器不信任这些 owner 请求头，而是从 OAuth 与 BKN Safe 重新构造 owner；任一授权依赖不可用时均拒绝写入，不得 fail-open。证据生产者则必须携带 ingest token；内部网络身份不能绕过该校验。

Chart 默认使用 memory store 且关闭 Core projection，以保证存量 trace/evidence 读取在普通 chart 升级时不会因缺少新 Secret 而中断。该默认值不代表具备 durable lifecycle；生产启用受管 Conversation / Interaction 时必须显式采用上面的 MariaDB 与 projection 配置。

### Durable answer handoff (#1691)

The opt-in managed `POST /interactions/{id}/finish` path accepts an
`answer_artifact` object using the existing EvidenceArtifact contract. Core
validates the result type, content hash, Interaction and trusted owner, then
commits the bounded answer task to the existing SQL outbox in the same
transaction as `execution_status=completed`. The response can therefore report
`evidence_status=assembling` while the conversation is already free for its next
Interaction. An outbox ACK means the answer is durably staged, not that the
artifact has reached OpenSearch.

After the artifact is durably acknowledged and its evidence state is settled,
the existing task-delivery UPDATE clears only the answer task's SQL body while
retaining delivery metadata. Pending/dead answers retain their bodies for
recovery. Delivered answers rely on the artifact store, as in the existing
synchronous path; the outbox is not a permanent second copy of the answer.

The existing leased worker delivers the artifact idempotently and separately
updates evidence state. Pending answers participate in every evidence-state
recalculation; exhausted delivery records a partial evidence result, never a
business failure or `abandoned`. Accepted tasks survive process restarts.
Archive cleanup refuses an Interaction while its answer remains unresolved or
its delivery task is not acknowledged. The existing artifact-ingest endpoint
retains its synchronous persistence ACK contract.

Both services default `BKN_TRACE_DURABLE_ANSWER_ENABLED` to false. Upgrade **all**
Core replicas/workers first, then enable Core with `core.durableAnswerEnabled`
(MariaDB, the OpenSearch evidence store and Core projection must be enabled), and finally enable Context
Loader with `observability.lifecycle.durable_answer_enabled`. Old workers must
not share a queue containing the new task type. The old reference-based finish
remains available. Turning off new admissions does not cancel accepted work;
drain/reconcile pending and dead-letter answer tasks before rolling Core back
to a version that cannot handle them.

If SQL/Core is unavailable, finish cannot acknowledge a durable completion;
retry the original finish request idempotently without repeating business work.
Ordinary list/detail reads do not upload artifacts or wait for this worker.

The production worker claims one ready task at a time and continuously drains
ready work; the polling interval applies only after an empty queue or error.
Default I/O budgets are 2s to claim, 10s for an answer attempt and 2s for its
independent ACK inside a 30s task lease. Startup requires positive budgets plus
a 1s safety margin to fit strictly inside that lease. Other handlers retain their
existing context and single-slot execution. The inserting SQL transaction checks
the actual packet budget; durable handoff requires `interpolateParams` absent or
false, preserving the prepared-statement size calculation.

See [managed completion limits](../../help/en/manual/bkn-trace.md#managed-interaction-completion-limits)
for product scheduling, size, policy, assembly and backlog limits. They do not
certify a fixed QPS or an environment-specific storage allocation.

### Interaction 容量配额

Core 在每个 Interaction 内强制限制 Operation、Claim 和已观察 evidence ref，默认分别为 `256`、`32` 和 `4096`。部署方可通过 Chart 的 `core.capacity` 修改它们；Chart 会分别注入 `BKN_TRACE_CORE_MAX_OPERATIONS_PER_INTERACTION`、`BKN_TRACE_CORE_MAX_CLAIMS_PER_INTERACTION` 和 `BKN_TRACE_CORE_MAX_EVIDENCE_REFS_PER_INTERACTION`。这些是仅服务端可控的启动配置，第三方 Agent 不能在请求中覆盖；变更后需要滚动 Core。

三个值必须为正整数，且 Claim 上限不能大于 Operation 上限、evidence ref 上限不能小于 Operation 上限。配置不合法时 Core 拒绝启动，避免静默失去配额保护。

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

Evidence、Business Graph、Snapshot、Node 和技术 Trace Graph 查询必须经过 OAuth 与 Access Profile 校验，并以有效主体、应用主体和资源授权在 OpenSearch 条件和返回层同时过滤。跨归属查询统一返回 404，不泄露 trace 是否存在。仅本地开发和测试可显式设置 `BKN_TRACE_ALLOW_UNAUTHENTICATED_QUERY=true`，Helm 默认关闭。

统一日志分页游标使用 `BKN_OBSERVABILITY_CURSOR_SIGNING_KEY` 做 HMAC 签名，并绑定过滤条件、主体、应用、Access Profile 指纹和可见来源。多副本部署必须通过 `observability.cursorSigning.existingSecret` 为全部 Pod 注入同一密钥；单实例本地环境未配置时使用仅在当前进程有效的随机密钥，进程重启后的旧游标按失效处理。密钥不得写入日志、响应或配置文件。

升级存量环境时，如果部署 values 覆盖了 `ingress.paths`，Helm 不会合并 chart 新增路径，必须显式补充 `/api/observability/v1`。扩容到多副本前也必须先配置共享的 `observability.cursorSigning.existingSecret`，否则不同 Pod 签发的游标不能互相验证。

Core 业务语义图的当前读取合同见前述“BKN Trace 3.0 Interaction 业务语义图”；
企业原生详情与解释见[读取边界](#community-与-ee-读取边界2517)。内部证据模型不代表
同名 HTTP 路由已挂载，旧 Evidence Chain / Evidence Node envelope 不再作为当前调用示例。

### 历史 Evidence Store 模型（非当前 HTTP 合同）

以下 2.0/2.1 聚合模型说明保留的旧存储实现，不是当前 `/evidence/events` 的 BKN Trace 3.0 写入合同，也不声明旧查询路由可用。

旧 Evidence ingestion 模型写入 `2.1.0`，读取兼容 `2.0.0`。2.1 事件执行精确 payload/ref allowlist、类型、枚举、hash、敏感值、trace/span/time join 校验。一般事实允许先于其父事件异步到达：首次查询返回 `causality_missing`，父事件补到后自动恢复完整；claim 的 `source_event_ids` 缺失仍返回 `source_event_missing`，Action 前态乱序继续原子拒绝。混合 2.0/2.1 历史按每个 event 的版本保留 `causality_missing`。

该旧 OpenSearch 模型使用“单 trace 聚合文档 + OCC”保障 Action 状态和事件冲突原子性。为避免文档无限增长，单 trace 硬限制为 10,000 个事件且聚合 JSON 不超过 8 MiB，超限返回 `BKN_TRACE_CAPACITY_EXCEEDED`。该限制不是最终扩展方案；后续需要迁移为事件文档加状态投影，并使用 PIT 分页。旧索引中缺少 account/application principal 必要归属的 2.0 聚合文档默认不可查询、不可继续追加；缺失归属必须通过离线受控迁移补齐，不能由请求方认领。

已记录的 Interaction、Operations 和业务语义组装修订通过前述 Core 读取入口查询；
EE 原生详情/解释需要实际装配的企业接口及相应许可、授权。两类响应不能互相替代。

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

`scripts/cleanup_legacy_bkn_trace_data.sh` 是早期 clean-slate 切换后退役旧目标的历史工具；它**不是** 0.1.3 → 0.1.4 的生产升级入口。该升级必须使用 [BKN Trace 0.1.3 升级到 0.1.4](../../help/zh/manual/bkn-trace-0.1.3-to-0.1.4-upgrade.md) 中的 `openbkn-deploy/scripts/upgrades/0.1.4/cleanup_legacy_bkn_trace_data.sh`，后者包含停写观察、集群上下文和依赖目标校验。

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

### Core Evidence 投影的 envelope 边界

Kafka Ledger 事件的 `envelope` 是生产者原始内容，Core 在 OpenSearch 投影中
使用 `object`、`dynamic: false` 保留完整 `_source`，不为任意嵌套内容自动
新增字段映射。查询使用顶层 ID、owner、状态和时间；020 完整性检查读取
权威 Ledger，而不是通过 envelope 子字段搜索推断记录完整。

新建版本索引应用该边界；已有投影 alias 的 bootstrap 在 envelope 类型兼容时
应用该边界。已有 alias 仅
合并兼容 mapping，不替换索引、不修改历史字段类型、不提高字段上限。
已有 envelope 字段仍按原映射索引；新字段只保存在 `_source`。既有字段的
类型冲突仍可能拒绝文档，本修复不自动重试已经标记 dead 的 outbox，也不
改写历史证据。未来需要检索的 envelope 字段必须按明确的产品需求显式定义。

已有 alias 升级前先只读每个具体索引的 mapping，并按索引分别应用兼容补丁。
未映射或 object envelope 接受上述 dynamic 边界；已映射 scalar/nested 类型或 `enabled:false` object
保持原状，仅更新会话审计字段并记录诊断，不会因强制改成 object 阻止 Core
启动。该历史索引仍不能存储与既有 envelope 类型不兼容的新文档。新索引的
object 边界也不承诺支持任意 scalar envelope。已有子对象显式 `dynamic:true`
覆盖父级的情况不会被递归重写；这些历史映射仍需单独评估。

### 完整性检查的预算停止

检查保留每个 Artifact HTTP 响应 8 MiB、每轮交互累计 32 MiB / 128 次读取的
既有上限。超过固定预算时，该源版本立即停止自动重试，诊断状态为
`exhausted`，`reason` 和 `last_failure_reason` 为
`inspection_budget_exceeded`。这表示尚未完成核验，不证明采集丢失，也不产生
`complete` 或 `missing` 判定。网络、超时等故障仍沿用现有退避重试。

新的真实源版本可重新检查；升级不会自动重置已耗尽的旧任务，也不会修改其
历史失败原因。不要为恢复检查而删除重试记录、伪造源版本或提高预算。
这些诊断仅由既有授权诊断接口读取，不增加普通日志页的内部服务状态。

### 显式重核超大历史记录

已耗尽的历史检查可使用 `cmd/record-integrity-recheck`，默认只读。输入为最多
100个显式Interaction ID的JSON数组，不扫描或重置全部历史任务：

```sh
# 使用现有数据库与证据配置；凭据通过环境变量传入，不写命令行。
go run ./cmd/record-integrity-recheck --ids-file /secure/interaction-ids.json
# 每条重新核验后，仅对仍为原源版本且已耗尽、尚无判定的记录原子保存。
go run ./cmd/record-integrity-recheck --ids-file /secure/interaction-ids.json --apply
```

必需环境变量为 `BKN_TRACE_CORE_MARIADB_DSN`、`OPENSEARCH_ENDPOINT`、
`OPENSEARCH_EVIDENCE_INDEX`；启用认证时使用既有
`OPENSEARCH_AUTH_ENABLED` / `OPENSEARCH_AUTH_USERNAME` / `OPENSEARCH_AUTH_PASSWORD`。
启用认证而缺少任一凭据时，命令在访问数据库前退出并指出所需配置项；
OpenSearch返回401/403时，该条输出`opensearch_auth_failed`，不输出凭据或上游响应正文。
命令不初始化数据库或索引，不改许可、业务事实、源版本、尝试次数或原失败原因。
`--apply`只在重新计算后保存判定并原子完成该代诊断任务；不是把原状态直接改成成功。

该路径串行处理一个Interaction、一个Artifact。先做有界元数据和所有者检查，
再用OpenSearch文档版本条件读取内容，逐字段规范化、验证完整内容SHA-256和长度，
最后执行原有完整性规则。正文不整体保留，也不使用脚本反复解析源对象。
每个对象最多读取32MiB（含元数据、转义和响应包裹），流缓冲32KiB，单字段
最多8MiB、嵌套128层；每对象25秒、每Interaction5分钟、最多512个唯一证据对象。
普通自动检查和Capture的原8MiB整包响应限制不变。

存储内容必须保持既有生产者`encoding/json`的键排序；非规范键顺序、重复键、
版本冲突、截断、哈希/长度/来源不符或资源超限均拒绝保存判定。输入/错误引用
仍使用原有有界读取路径。该维护工具不把未知或真实缺口当成完整，也不补造
历史调用结束结果；SQL快照仍复用既有实现，命令应在具备数据库维护权限的
受控环境中串行运行。报告可能含内部记录ID，应按现场诊断文件保护。

### MariaDB 投影领取性能回归

在本模块目录运行 `make test-outbox-performance`，设置
`BKN_TRACE_OUTBOX_PERF_ADMIN_DSN` 为隔离 MariaDB 11.4 测试服务器的 admin DSN
（含 `parseTime=true&multiStatements=true`，允许创建和删除数据库）。测试创建并
清理独立数据库，使用生产 Core 建表脚本，不修改共享审计库。CI 使用相同入口，
缺少 DSN 时 Makefile 直接失败，避免把跳过测试当作通过。

历史数据集包含 190,148 条终态任务和 14,213 条 pending，最早 pending ID 为
190149，每条 payload 约 1 KiB；建议测试服务器使用 128 MiB InnoDB buffer pool。
测试输出候选查询与真实 `FOR UPDATE SKIP LOCKED` 查询的 EXPLAIN；候选分支须使用
claim-order 覆盖索引且不对大表排序，加锁查询须使用限定候选 ID 的主键路径。
计时覆盖完整 `Lease()`：事务开始、数据库时钟、候选发现、加锁重检、payload 读取、租约更新和提交；
20 次单条领取、100 条批量领取和空队列领取均须在独立的 2 秒 deadline 内完成。
另有跨状态 ID 顺序、跨页锁定前缀补领、候选变更重检、延迟任务重新可用、
并发领取、过期租约恢复、旧 token fencing 及有数据索引迁移幂等回归。
结果用于约束代表性数据下的领取路径，不代表其他硬件或任意积压规模的耗时保证。

领取依赖 v040 新增的 `(status, outbox_id, available_at, locked_until)` 覆盖索引。
每个状态最多取 128 个候选 ID，仅合并排序至多 256 个 ID；之后按主键加锁重检。
遇到行锁或过期候选时通过 ID 游标补页，每次新领取从头开始，避免遗漏较早的重试任务。
一次领取共用数据库时间快照，保持跨状态和补页时的时间资格一致。原 pending 索引保留。
该路径仍可能扫描大量时间未到或有效租约的索引项，不承诺任意分布下的恒定扫描量。
升级与恢复需同时核对物理索引和迁移账本，见下文“v040 升级与恢复约束”。

更大积压的容量实验使用 `make test-outbox-scale`，另需设置
`BKN_TRACE_OUTBOX_SCALE_OPENSEARCH_URL` 为隔离 OpenSearch 实例。该入口独立运行，
创建约 19 万条历史终态任务及 10 万／50 万条全部到期的 pending，
分别采样 20 次单条和 10 次百条完整领取事务，输出延迟分位数、错误及超时次数。
随后使用生产 `projectorsvc.Worker`（BatchSize=1、领取超时 2s、lease 30s），
分别连接空 sink 和真实 OpenSearch sink，测量交付吞吐及领取／投影／ACK 耗时占比。
空 sink 只测量 SQL 消费能力，不作为端到端吞吐；真实 sink 的 SQL 交付数与
OpenSearch 文档数须一致。默认每种 sink 测量 30s，可用
`BKN_TRACE_OUTBOX_SCALE_WORKER_DURATION=60s` 延长，每 10s 输出一个窗口。
每种 sink 测量前会在计时之外恢复合成夹具到指定 pending 数量，并核对交付数与
实际剩余 pending 的差值。CI 独立容量步骤运行相同数据集，每种 sink 测量 10s；
本地持续吞吐验证建议使用 60s。夹具恢复不用于生产数据。
采样边界在事务之间检查，避免主动取消最后一笔事务制造假超时。
2s 门槛失败仍会继续收集后续阶段数据，最后以失败状态退出，不把容量问题静默通过。

`make test-outbox-million` 运行同一压测的 100 万 pending 数据集；也可通过
`BKN_TRACE_OUTBOX_SCALE_SIZES=1000000` 或逗号分隔列表指定 1～500 万的夹具规模。
该参数不改变默认 CI 的 10 万／50 万数据集。

`make test-outbox-soak` 运行持续生产与消费实验，默认每档 5 分钟，共 30 分钟。
阶梯为 1、2、5、10、20、40 次生命周期调用／秒，分别等价于 10～400 个每 10 秒
调用一次的活跃合成 Agent。每个 Agent 复用会话，每轮真实调用 Session 服务的
Start、EnsureOperation、CompleteOperationAttempt、Terminate，输入和结果约 1 KiB，
答案约 16 KiB；真实单槽 worker 处理投影和答案持久化到 OpenSearch。
这不包含 LLM、MCP 网络传输、鉴权、真实业务工具、完整应用的租约回收和失败补偿，
不能直接视为完整 Agent 并发上限；失败调用遗留 active interaction 会影响后续档位。
测试先校准每次调用的任务数（含答案落库后的追加任务），再每 30 秒记录实际流入、
交付、backlog、最早 pending 年龄、领取与交付分位数。另每约 5 秒探测一次自然
refresh 后的 OpenSearch 搜索可见性，20 秒超时单独计数，不主动刷新索引。
高负载积压不会在阶段之间清空；测试完成代表实验执行成功，不代表所有负载档满足 SLO。
`BKN_TRACE_OUTBOX_SOAK_STAGE_DURATION` 可覆盖每档时长，短测仅用于校验测试工具，
正式持续负载实验应保留 5m。长测不加入常规 CI，需显式 opt-in、隔离数据库与索引。

### v040 升级与恢复约束

`GET_LOCK(..., 30)` 的 30 秒是其他启动实例等待迁移建议锁的预算，不是索引构建上限。
当前 Chart 的 startup probe 为 `periodSeconds: 10`、`failureThreshold: 12`，启动窗口约两分钟。
大 outbox 的索引构建若超过这些窗口，等待锁的副本可能启动失败，迁移实例也可能被重启。
不能仅凭缩为一个副本就认为长 DDL 已被解决。

升级前应在代表性数据上评估索引构建时长、空间、I/O 与 metadata lock 影响，并安排受控窗口。
需要预建索引时，由运维按
[原始 v040 SQL](https://github.com/openbkn-ai/bkn-foundry/blob/6c0da73e31e5b7e26441336ea7bc0dfbca256370/bkn-trace/agent-observability/migrations/mariadb/v040/init.sql)
创建并核对索引名、列顺序和定义；不要让多副本
同时承担首次大表 DDL。之后由一个受控升级实例沿现有允许自动迁移的启动流程完成迁移
清单校验和账本登记，确认就绪后再将其余副本升级为同一迁移兼容镜像并恢复自动伸缩。
该实例正常启动后可能消费任务，
应纳入升级影响范围。若不预建索引，单实例迁移仍必须具有覆盖实测 DDL 耗时的启动窗口；
不能假设当前 Chart 提供了可直接设置的探针时长参数。

预建索引后，`CREATE INDEX IF NOT EXISTS` 可跳过同名索引的重复创建，**账本登记仍必须完成**。
只手工建索引、未登记 `040`，在 `autoMigrate=false` 的环境仍会报 schema 落后并拒绝启动。
不得手工伪造校验和来代替正常迁移。在线 DDL 仍有 metadata lock 和资源开销，不能承诺零写入影响；
参见 [MariaDB CREATE INDEX](https://mariadb.com/docs/server/reference/sql-statements/data-definition/create/create-index)。

v040 写入 `bkn_trace_schema_migrations` 后，不包含该版本的旧镜像会报
`schema migration 040 is newer than this BKN Trace image`，即使索引保留且 SQL 向后兼容，
也不能只回退镜像；关闭自动迁移同样不会绕过账本检查。部署系统的自动镜像回退也受此约束。

本变更默认采用**兼容当前账本的前滚修复**。若必须恢复旧版本，应使用升级前一致性备份
及经过验证的恢复方案，评估升级后写入的恢复点/数据损失，并在独立批准的维护窗口执行。
不将删除单条迁移账本记录作为常规回滚步骤，也不关闭版本/校验和校验。
仍有新代码运行时不得先删索引，否则 `FORCE INDEX` 领取会失败。

### Issue #2585 开发环境测量记录

以下为特定开发环境的可复现记录和待验证候选设置，不是产品手册的生产并发等级。
相关实现与复现入口见 [PR #2597](https://github.com/openbkn-ai/bkn-foundry/pull/2597)
及本节之前的 `make test-outbox-*` 说明。

| 项目 | 本次实测 | 与生产配置的差异 |
| --- | --- | --- |
| Docker VM | 6 vCPU、约 15.6 GiB，共享已有开发负载 | 非独占生产环境 |
| Trace/负载驱动 | 宿主 Go 进程，无 Pod CPU 硬限制 | Chart 默认 requests 100m/128Mi、limits 500m/512Mi |
| MariaDB | 11.4.13、buffer pool 128 MiB | 不是生产内存推荐值 |
| Core 连接池 | 最大 32、Go 默认最多 2 idle | 生产代码为 16 open / 4 idle |
| OpenSearch | 2.19.4、heap 512 MiB、容器上限 1.5 GiB | 测试配置 |
| 投影 worker | 单个、BatchSize=1、领取预算 2s、lease 30s | 未验证水平扩容容量 |

各规模另有 190,148 条历史终态记录；每种 sink 前在计时外恢复准确 pending 数量。
完整领取包含 BeginTx、数据库时间、候选、加锁重检、payload、租约更新和 Commit。

| 初始 pending | 单条采样最大（20 次） | 百条采样最大（10 次） | 真实 OpenSearch worker（每阶段 60s） |
| --- | ---: | ---: | ---: |
| 100,000 | 4.95ms | 57.04ms | 237.1 任务/s |
| 500,000 | 7.11ms | 80.00ms | 82.2 任务/s |
| 1,000,000 | 22.46ms | 155.23ms | 50.5 任务/s |

上述实验均无领取超时，SQL 交付量、pending 减少量与 OpenSearch 文档数一致。
前一轮没有逐阶段恢复积压的有序领取测试同样通过，但吞吐不同；这里采用准确恢复规模的
最终复测数据。宿主负载、缓存和实验配置有波动，不能外推为稳定生产 SLA。

持续负载实验总计约 30 分钟，每档 5 分钟、阶段间不清积压。模型为每个合成 Agent 每 10 秒
一轮、每轮一个工具调用、约 1 KiB 输入/结果、16 KiB 答案，复用会话一轮实测 10 条任务，
首次新会话 12 条，均包含答案持久化后的追加任务。

| 活跃 Agent 等价 | 目标调用/s | 发生器发起 | 调用失败 | pending 净增加 | 阶段末 pending |
| --- | ---: | ---: | ---: | ---: | ---: |
| 10 | 1 | 300 | 0 | 8 | 8 |
| 20 | 2 | 599 | 0 | 29 | 37 |
| 50 | 5 | 1,500 | 0 | 6 | 43 |
| 100 | 10 | 2,994 | 1 | 6,133 | 6,176 |
| 200 | 20 | 5,963 | 1,128 | 25,646 | 31,821 |
| 400 | 40 | 11,785 | 6,437 | 33,541 | 65,361 |

最后一档丢弃 82 个无法排入的请求，实际处理 11,703 个；阶段间交接会有在途任务。
50 档查询可见性抽样 p95 约 2.19s、p99 约 4.21s；100 档起出现大量超过 20s 的探测超时。
高档不能仅用少量成功探测的分位数判断可用性。长测领取零超时，最慢约 1.13s，投影重试/
死信均为零；这些指标不等于业务容量充足。每档仅五分钟也不构成长期稳态容量认证。

实验只覆盖 Session 服务、SQL、worker、真实 OpenSearch 与答案持久化，未覆盖完整
MCP/HTTP、鉴权、LLM、真实业务工具、Evidence Kafka、租约回收器及失败补偿。
生命周期死锁重试耗尽和遗留 active interaction 会影响后续高档错误率，不能当作生产错误率。
100 万静态积压实验与持续流入实验分开执行，未验证两者同时施压的查询新鲜度。

以下候选资源数值未经上述限额复测，不是默认配置、Agent 等级或容量承诺：

| 组件 | 下一轮验收候选 | 需要观察 |
| --- | --- | --- |
| agent-observability | 1 副本；requests 1CPU/512Mi；limits 2CPU/1Gi | throttling、RSS、请求与消费延迟 |
| MariaDB 专用预算 | 2vCPU/4Gi，buffer pool 先 2Gi，SSD | 工作集、物理读、fsync、锁和连接等待 |
| OpenSearch 单节点 | 2vCPU/4Gi，heap 2Gi，SSD | heap/GC、索引、refresh 与查询延迟 |
| Core 池与 worker | 先按生产 16 open/4 idle、单 worker 验收 | 多副本连接与锁竞争、恢复余量 |

资源规划方法和验收维度见
[容量参考手册](https://github.com/openbkn-ai/bkn-foundry/blob/main/help/zh/manual/bkn-trace-resources.md)。
本记录不包含完整平台安装规格，不代表已完成生产 DDL、上线或现场 Trace 恢复验收。
