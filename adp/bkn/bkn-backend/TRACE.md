# bkn-backend BKN Trace 接入合同

> 状态：BKN Trace 2.1 producer 实施基线
> 适用版本：`bkn.trace.schema.version=2.1.0`
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 模块责任

- 模块名：`bkn-backend`。
- 事实边界：对象类型、关系类型、行动类型和指标定义等知识网络 schema 读取。
- 标准事实事件：`knowledge.read.observed`。
- 本模块只记录读取事实，不解释结果、不创建结论，任何路径均不得生成 `claim.created`。

## 上下文合同

入站接受 `traceparent`、`bkn-request-id`、`x-request-id`，以及：

```text
bkn-interaction-id
bkn-operation-id
bkn-causation-event-id
bkn-claim-id
```

- `interaction_id`、`operation_id` 缺失时为本次读取生成安全 ID。
- `attempt` 由 producer 写入事件并默认归一为 `1`，不新增跨服务 header。
- 没有直接上游业务事件时允许不设置 `causation_event_id`。
- `bkn-claim-id` 只能表示上游已存在的 claim，不得在本模块内合成。

## 事件规则

| 条件 | 事件 | 约束 |
| --- | --- | --- |
| schema 读取成功 | `knowledge.read.observed` | payload 仅含 `kn_id`、`read_kind`、`version_status` |
| 同时收到上游 `claim_id` 且存在受控引用 | `evidence.refs.created` | 关联读取事实，不含原始 summary/schema |
| 同时收到上游 `claim_id` 且存在受控引用 | `business.refs.resolved` | 解析为 object/relation/action/metric 业务引用 |
| 收到上游 `claim_id` 但没有可解析引用 | `business.refs.resolved` | `resolver_status=unresolved`，引用数组为空 |
| 没有上游 `claim_id` | 不生成 refs 事件 | 防止把读取结果伪装成结论证据 |

`knowledge.read.observed.payload` 只允许 `kn_id`、`read_kind`、`version_status`、可选 `schema_version/business_refs`；当前实现只写前三项。引用只允许：`ref_id`、`ref_type`、`source_system`、`summary_hash`、`validity`、`version_status`、`visibility`。所有 hash 均为 `sha256:<64 位小写十六进制>`。当前 schema 版本锚点尚未接入，统一标记 `unversioned`。

## 安全边界

不得写入名称、注释、属性名、映射规则、行动意图、指标公式、SQL、行数据、prompt、工具输入输出、凭据、Cookie、token、连接串或对象存储裸 URL。提交由 `BKN_TRACE_EVIDENCE_INGEST_URL` 控制，异步且 fail-open，不改变业务响应。

## 验收

- fixture：`fixtures/bkn-trace/phase2/bkn_schema_l2_positive.json`。
- Given 无上游 claim，When schema 读取成功，Then 仅生成一个 `knowledge.read.observed`。
- Given 有上游 claim，When schema 读取成功，Then 追加受控 evidence/business refs，且所有 refs 绑定该上游 claim。
- Given schema 含敏感文本，When 构造事件，Then payload 不包含原始 schema 或 `summary`。

## 已知限制

- schema/snapshot 不可变版本尚未接入，因此当前为 `unversioned`。
- 跨模块图组装、partial 计算、快照持久化与 Studio 展示由 BKN Trace 核心负责。
