# mf-model-api BKN Trace 接入合同

> 状态：BKN Trace 2.1 生产者实施基线
> 更新时间：2026-07-25
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 一、模块责任

- 模块名与观测服务：`mf-model-api`。
- OpenAI、Claude、Baidu、Baidu Tianchen 和其他实际模型分支统一记录 `model.call.observed`，覆盖普通与流式响应。
- 本模块只记录模型调用事实，不替 Agent/AI 应用创建结论、证据引用或业务引用。
- 成功构造模型事实后，通过响应头 `bkn-evidence-event-id` 返回稳定事件 ID；同时将请求中安全校验且实际进入模型上下文的 `bkn-candidate-source-event-ids` 作为 `bkn-adopted-source-event-ids` 回执，供上层创建结论时绑定来源。

## 二、上下文与重放

上游必须传入合法 trace/request，以及 `bkn-interaction-id`、`bkn-operation-id`、`bkn-causation-event-id` 和 `bkn-event-observed-at`；`bkn-attempt` 默认 `1`。

- `bkn-event-observed-at` 是首次业务调用创建的 UTC RFC3339Nano 时间，重试和跨进程重放必须复用。
- 缺少任一业务因果字段或可靠 observed time 时不创建业务事实，只保留技术日志/trace。
- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)` 的完整 64 位十六进制结果。
- 不能仅复用 operation 并用当前时间重建事件；生产者不持久化 envelope，这种重启场景明确不受支持。

## 三、事件与敏感边界

payload 精确允许：

```text
model_name
model_provider
status
input_token_count
output_token_count
prompt_hash
output_hash
error_category（错误时）
error_hash（错误时）
```

完整 prompt/messages、模型输出、工具 schema/参数/结果、供应商错误、PII、API key、Authorization 和 Cookie 均不得进入事件、普通日志或 span。成功与失败仅记录哈希、计数和安全枚举。

## 四、可靠性

- ingest HTTP 非 2xx 视为失败，最多尝试三次。
- 异步任务保留强引用；最终失败记录 warning，模型业务响应保持失败开放。
- 当前没有持久 outbox，进程退出可能丢失未提交事件；完整可靠重放由调用方保存并复用 envelope 后主动重试。

## 五、验收

- fixture：`fixtures/bkn-trace/phase2/mf_model_call_l2_positive.json`。
- Given 任一实际模型 provider，When 普通或流式调用结束，Then 使用同一 producer 合同产出成功或失败事实。
- Given 相同完整 envelope 重放，Then event ID 和 observed time 不变。
- Given 缺少 `bkn-event-observed-at`，When 模型结束，Then 不产生会冲突的新事件。
- Given 非 2xx ingest，When 上报，Then 重试三次并记录最终失败。

## 六、已知限制

模型模块不保存 prompt/output 快照，也没有持久 outbox；结论绑定、业务语义解析和快照由上层 Agent/AI 应用与 BKN Trace 核心负责。
