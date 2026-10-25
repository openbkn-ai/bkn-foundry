# BKN Trace API 文档

- [OpenAPI JSON](./swagger/swagger.json)
- [OpenAPI YAML（统一发布入口）](../../../docs/api/agent-observability/agent-observability.yaml)

接口文档由代码注解生成。JSON 与运行时生成代码保留在模块内，供服务内置 Swagger 使用；对外发布的 YAML 统一存放在仓库根目录 `docs/api/`，并由同一生成命令同步和校验，禁止人工维护副本。

BKN Trace 的需求、设计与实施计划统一维护在 `bkn-docs/docs/foundry/bkn-trace/`。

## Audit publisher diagnostics (#2109)

Operation-audit `source_status` on `/api/observability/v1/logs` and source inventory separates Ledger query availability (`status_scope: query`) from `collection_status`. The latter describes only one observed Safe publisher instance; it does not certify all replicas, consumer delivery, or historical completeness.

- `collection_status.status`: `unknown`, `healthy` (actual delivery observed), or `degraded` (confirmed publisher failure/drop).
- Query `partial` and count accuracy describe the existing query/source result contract. Publisher failure and historical loss remain independent diagnostics: neither permanently makes every query partial nor changes the precision of a stored-record count. Existing query failures and legacy source-coverage degradation retain their behavior.
- Missing observations, failed probes and observations older than 90 seconds are unknown. Empty/authorized-filtered query results and an idle publisher are not failure signals. Enqueue acceptance is not delivery recovery.
- `collection_status.dropped_records` is the current instance's observed counter. `null` means unknown, not zero. `historical_gap` is separate durable evidence of previously observed loss. Its `historical_gap_since` is the first observation time, not the exact beginning of missing events. Recovery never backfills those records; `null` does not prove complete history.

The existing source-level `dropped_records` / `dropped_records_since` fields describe per-source/deployment coverage observations, not counts filtered by the current query. They are independent of publisher-instance counters and gap timestamps. A publisher gap with an unknown observation time cannot borrow a source-level timestamp.

Configure `observability.auditProducerStatusEndpoint` in the Agent Observability Chart (or `BKN_OBSERVABILITY_AUDIT_PRODUCER_STATUS_ENDPOINT`) with a stable cluster-internal Safe-instance URL such as `http://safe-instance:3000/internal/audit/publish-status`. Deploy the Safe version supporting that read-only endpoint first. The endpoint contains aggregate status only, follows the existing internal network trust boundary, and must not be exposed through ingress. One endpoint does not aggregate replicas.

The optional monitor polls every 30 seconds, with a 3-second whole-observation deadline and a 64 KiB response limit. HTTP redirects and URL credentials are rejected. Query handlers only read a cached snapshot. Historical-loss evidence reuses the existing source-coverage table; its dedicated gap row is not a cross-instance cumulative counter. An empty endpoint disables network observation and leaves publisher status unknown. No production configuration or migration is applied by this change.
