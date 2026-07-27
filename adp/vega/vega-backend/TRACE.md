# vega-backend BKN Trace 接入合同

> 状态：BKN Trace 2.1 生产者实施基线
> 更新时间：2026-07-25
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 一、模块责任

- 模块名：`vega-data`，观测服务：`vega-backend`。
- 资源元数据和资源数据查询记录为 `data.query.observed`。
- 模块只记录数据访问事实，不创建 claim，不在事实阶段生成结论绑定事件。

## 二、上下文与子操作

事实生产要求合法 trace/request/account、`bkn-interaction-id`、`bkn-operation-id`、`bkn-attempt` 和明确传播的 `bkn-event-observed-at`。业务因果 ID 不要求固定前缀，只校验安全字符与长度。

- `business_domain`、attempt、observed time 在可信内部调用中传播。
- 调用 permission、model-factory 等下游时 fork 子 `operation_id`，保留直接 cause。
- 子 operation 派生包含父 operation、稳定操作名、attempt 和显式调用序号。同一父 operation 下同名多次调用必须递增序号或显式提供子 operation。
- 不可信出站必须剥离业务因果、业务域与 observed time。`bkn-agent` 接入路径不在本变更范围。

## 三、事实、引用与安全

`data.query.observed.payload` 只允许 `query_hash/query_type/row_count/truncated/as_of/version_status/resource_refs/field_refs`。

- `resource_refs` 使用受控资源 ID。
- `field_refs` 使用资源 ID 加业务字段标识的 hash，不输出物理字段名。
- 不生成 row ref，不把行内容、行 hash、完整 SQL、SQL 参数、过滤值、连接串或 PII 写入 event/log/span。
- 即使有上游 claim，也只产生一个数据查询事实；Agent/AI 应用负责后续结论绑定。

## 四、事件身份与可靠性

- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)`。
- `observed_at/emitted_at` 复用首次调用的稳定 envelope；缺少或非法 `bkn-event-observed-at` 时不生成事实，不能回退到 `time.Now()`。
- 事实 ID 通过响应头 `bkn-evidence-event-id` 返回。
- 上报最多三次，HTTP 非 2xx 判失败；队列满与最终失败必须记录日志，业务请求保持失败开放。
- 当前只有有界内存队列，没有持久 outbox；进程退出可能丢事件，跨进程重放依赖调用方复用完整 envelope。

## 五、验收

- fixture：`fixtures/bkn-trace/phase2/vega_data_evidence_l2_positive.json`、`data_query_observed_2_1_positive.json`。
- Given 查询结果含行内容，When 构造事件，Then 行内容不影响引用且不进入 payload。
- Given 资源 schema 含物理字段名，When 生成 field ref，Then 只出现不可逆受控标识。
- Given 同一父 operation 下同名多次下游调用，When 使用不同 ordinal，Then 子 operation 不碰撞。
- Given 非 2xx ingest，When 上报，Then 有界重试三次并可观测失败。

## 六、已知限制

资源不可变版本、证据快照和持久 outbox 尚未接入；全局证据图和可视化由 BKN Trace 核心负责。
