# ontology-query BKN Trace 接入合同

> 状态：BKN Trace 3.0 Producer Outbox 实施基线
> 更新时间：2026-08-03
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 一、模块责任

- 模块名：`bkn-ontology`，服务名：`ontology-query`。
- 对象实例、关系路径和指标查询记录为 `data.query.observed`；schema 定义读取由 `bkn-backend` 记录。
- 本模块只记录查询事实，不创建或绑定 claim。Agent/AI 应用形成结论后，使用返回的事实事件 ID 建立证据关系。

## 二、上下文、子操作与重放

入站要求 trace/request/account、`bkn-interaction-id`、`bkn-operation-id`、`bkn-attempt` 和 `bkn-event-observed-at`；有直接原因时携带 `bkn-causation-event-id`。ID 不要求固定前缀，只校验安全字符和长度。

- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)`。
- 调用方必须在首次调用时确定并在重放时复用完整 envelope，尤其是 `observed_at`。
- 缺少或非法 `observed_at`、interaction 或 operation 时不产生核心事实，不能用 `time.Now()` 重建同 ID 的新事件。
- 事实 ID 通过响应头 `bkn-evidence-event-id` 返回。
- 下游调用应派生新的 child `operation_id`，保留直接 `causation_event_id`；同一父 operation 下同名多次调用必须使用稳定调用序号或调用方显式提供子 operation，不能默认复用造成碰撞。

## 三、事实与受控引用

`data.query.observed.payload` 仅允许 `query_hash/query_type/row_count/truncated/as_of/version_status/resource_refs/field_refs`。

- 对象、关系路径和指标来源进入 `resource_refs`，但保持真实 `ref_type=object|relation|metric`；注册业务字段进入 `field_refs`。
- BKN 引用必须全限定为 `object:<kn_id>:<object_type_id>`、`relation:<kn_id>:<relation_type_id>`、`metric:<kn_id>:<metric_id>`；不得产生 `object_type:<id>` 等无法授权解析的短引用。
- 不从返回行生成引用，不记录对象实例内容、关系实例内容、指标值、物理表名或物理字段名。
- 引用仅允许 `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash（可选）`。
- 即使收到上游 claim，本模块也只生成查询事实，不在事实阶段追加 claim 绑定事件。

## 四、可靠性与安全

- 启用 `BKN_TRACE_OUTBOX_ENABLED=true` 后，3.0 事实先写入本地 Producer Outbox，再由 Worker 异步投递 Core。
- Outbox 使用 **Deployment + 固定 `producer_stream_id`**（`BKN_TRACE_PRODUCER_STREAM_ID`，默认 `ontology-query`）；`producer_id` 仍为 `bkn-ontology`。
- 单 Pod 可同时写并投递；多 Pod 时 API 只写、单独 Worker 投递，共用同一 stream ID。
- Worker 启动递增 epoch；Enqueue 在事务内读取当前 epoch。
- 禁止 SQL、参数、查询正文、行数据、PII、prompt、工具输入输出、凭据和裸 URL。
- 未启用 Outbox 或未跑 migration 时不产出 3.0 事实。

## 五、验收

- fixture：`fixtures/bkn-trace/phase2/ontology_data_evidence_l2_positive.json`。
- Given 对象/关系/指标查询，When 构造事实，Then payload 只含查询摘要、计数和受控 resource/field refs。
- Given 返回结果含敏感行内容，Then行内容及物理名不进入事件。
- Given 相同完整 envelope 重放，Then event ID 与时间戳一致；缺少原始 observed time 时拒绝产出。

## 六、已知限制

数据快照版本尚未接入，当前为 `unversioned`；全局证据图由 BKN Trace 核心组装。

## 七、Producer Outbox 部署要点

与 `bkn-backend` 相同模式：`producerStreamID=ontology-query`，migration 名为 `ontology-query-trace-outbox`。
