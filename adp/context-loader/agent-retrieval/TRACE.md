# context-loader BKN Trace 接入合同

> 状态：BKN Trace 2.1 生产者实施基线
> 更新时间：2026-07-25
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 一、模块责任

- 模块名：`context-loader`。
- 观测操作：`context.search_schema`、`context.query_object`、`context.query_instance_subgraph`。
- 标准事实：`retrieval.completed`。
- 模块只陈述检索事实，不生成 claim，也不根据上游 claim 在事实阶段生成 `evidence.refs.created/business.refs.resolved`。结论和绑定由 Agent/AI 应用在得到真实事实事件 ID 后完成。

## 二、入站上下文

接受 `traceparent`、`bkn-request-id/x-request-id`、account/auth 头和：

```text
x-business-domain
bkn-conversation-id
bkn-interaction-id
bkn-operation-id
bkn-causation-event-id
bkn-claim-id
bkn-attempt
bkn-event-observed-at
```

- `business_domain` 来自 `x-business-domain` 或受控 baggage 的 `business_domain`，不得错误使用 `account_id` 代替。
- conversation/interaction 由调用方拥有；Context Loader 仅校验、透传和记录，缺失时不生成，`Mcp-Session-Id` 也不得替代业务 conversation。
- 业务因果 ID 只校验安全长度和字符，不强制固定前缀；非法值在边界丢弃。
- `bkn-event-observed-at` 只有在入站明确提供且为 UTC RFC3339Nano 时标记为可重放；本地为技术日志生成的当前时间不能用于核心事实。
- 缺失 interaction、operation 或可靠 observed time 时，不生成核心事实。

## 三、下游子调用合同

每次 BKN、ontology、Vega、模型、Operator/MCP 调用必须 fork 新 `operation_id`，并保持直接 `causation_event_id`。派生算法纳入父 operation、稳定操作名、attempt 和显式 `callOrdinal`。

- 同一父 operation、同名调用、同一 ordinal 的重放得到相同子 operation。
- 同一父 operation 下同名多次调用必须递增稳定 ordinal，或显式传入唯一子 operation；禁止全部使用同一 ID。
- `attempt`、`business_domain` 和可靠 `bkn-event-observed-at` 随可信内部调用传播。
- 调用不可信第三方前必须剥离 OpenBKN 业务因果、业务域和 observed time。

## 四、事实与引用

`retrieval.completed.payload` 精确为 `query_hash/candidate_count/truncated/version_status/source_refs`。

- schema 检索保留受控对象、属性、关系、指标、行动引用。
- 对象查询只保留请求中的对象类型和业务属性引用，不使用返回行生成 row ref。
- 子图查询只根据请求的对象类型和关系类型路径形成引用，不从返回节点/边内容推断。
- 引用只含 `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash（可选）`，不得含 query、条件值、属性值、名称、物理名或行内容。

## 五、事件身份与可靠性

- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)`。
- `observed_at/emitted_at` 复用调用方 envelope 的稳定时间。
- 发射函数显式返回真实 event ID；HTTP 对象查询同时返回 `bkn-evidence-event-id`。
- 上报最多三次，HTTP 非 2xx 判失败；队列满和最终失败记录 warning，不阻塞业务。
- 当前只有有界内存队列，没有持久 outbox。进程退出可能丢事件；跨进程可靠重放依赖调用方持久并复用完整 envelope。

## 六、验收

- fixture：`fixtures/bkn-trace/phase2/*_positive.json`。
- Given `business_domain != account_id`，When 传播和产出事件，Then 使用真实业务域。
- Given 同一父 operation 下两次同名调用，When ordinal 分别为 1、2，Then 子 operation 不碰撞；重放 ordinal=1 保持稳定。
- Given 返回行含 PII，When 生成 source refs，Then 不出现 row ref、属性值或物理字段名。
- Given 只有 operation 而缺少原始 observed time，When 重建事实，Then 不产出冲突事件。

## 七、已知限制

版本化引用、持久 outbox 和快照仍由后续工作承担；`run_sql` 已记录安全查询摘要、结果规模和业务资源引用，全局图与跨 request/trace 的 Interaction 聚合由 BKN Trace Core 承担。
