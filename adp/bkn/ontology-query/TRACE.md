# ontology-query BKN Trace 接入合同

> 状态：BKN Trace 2.1 producer 实施基线
> 适用版本：`bkn.trace.schema.version=2.1.0`
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 模块责任与事件归类

- 模块名：`bkn-ontology`，服务名：`ontology-query`。
- 对象实例、关系路径和指标值查询返回的是业务实例数据，不是 schema 定义，因此记录为 `data.query.observed`。
- schema/type 定义读取由 `bkn-backend` 记录为 `knowledge.read.observed`。
- 本模块只记录查询事实，不解释查询结果、不创建结论，任何路径均不得生成 `claim.created`。

## 上下文传播

入站解析并在出站 HTTP 调用中传播：

```text
traceparent
bkn-request-id
x-request-id
bkn-interaction-id
bkn-operation-id
bkn-causation-event-id
bkn-claim-id
```

`interaction_id`、`operation_id` 缺失时生成；`attempt` 由 producer 写入事件并默认为 `1`，不新增跨服务 header。`baggage` 仅保留 `bkn.account.type`、`bkn.runtime.env`，不得作为授权来源。Vega 出站调用统一使用 `common.MergeTraceHeaders`，传播三个因果 header，传播值不包含 SQL、查询参数、行数据或凭据。`bkn-claim-id` 仅在上游已有 claim 时作为条件关联，不得由读取模块生成。

## 事件规则

| 操作 | 事件 | query_type |
| --- | --- | --- |
| 对象实例查询 | `data.query.observed` | `object_instance` |
| 子图/关系路径查询 | `data.query.observed` | `relation_path` |
| 指标查询/试算 | `data.query.observed` | `metric` |

`data.query.observed.payload` 仅含 `query_hash`、`query_type`、`row_count`、`truncated`、`version_status`。完整 query、SQL、参数与返回行不进入事件。

只有收到上游 `bkn-claim-id` 时，才在读取事实之后生成：

- `evidence.refs.created`：受控 row/schema/metric 引用，允许 hash、状态和可见性，不含 `summary` 或行结构。
- `business.refs.resolved`：将引用解析为 object/relation/metric 业务引用。

收到上游 claim 但没有可解析引用时，只追加 `business.refs.resolved`，设置 `resolver_status=unresolved` 且引用数组为空；不得伪造 ref。

没有上游 claim 时仅生成查询事实，避免把“读到了数据”错误表达为“形成了结论”。

## 安全边界

禁止完整 SQL、SQL 参数、对象实例属性、指标 label/value、PII、prompt、工具输入输出、token、Cookie、Authorization、连接串和对象存储裸 URL。`safeObjectQueryShape` 等逻辑只对受控查询形状求 hash；引用严格只保留 `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash`，所有 hash 均为 `sha256:<64 位小写十六进制>`。

## 验收

- fixture：`fixtures/bkn-trace/phase2/ontology_data_evidence_l2_positive.json`。
- Given 无上游 claim，When 对象/关系/指标查询成功，Then 只生成 `data.query.observed`。
- Given 有上游 claim，When 查询成功，Then 追加受控 evidence/business refs，并绑定同一 claim。
- Given 上游三类因果 header，When 调用 Vega，Then trace/request/interaction/operation/causation 保持传播。
- Given 查询与结果含敏感内容，When 构造事件，Then payload 只出现 hash、计数、枚举与受控引用。

## 已知限制

- 数据快照锚点尚未接入，当前引用为 `unversioned`。
- 当前已有 Vega 调用使用统一传播工具；其他新出站调用必须复用 `common.MergeTraceHeaders`。
- 跨模块图组装、partial 计算、快照持久化与 Studio 展示由 BKN Trace 核心负责。
