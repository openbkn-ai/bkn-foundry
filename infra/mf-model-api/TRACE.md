# mf-model-api BKN Trace 接入规范

> 状态：2.1.0 实施基线
> 更新时间：2026-07-25
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/BKN Trace 设计.md`

## 1. 模块责任

- 模块名与观测服务：`mf-model-api`。
- 运行形态：Python FastAPI 服务。
- 代码路径：`infra/mf-model-api`。
- 写入合同：`bkn.trace.schema.version=2.1.0`。
- 本模块只陈述“发生了一次模型调用”的运行事实，不替 Agent 或 AI 应用创建业务结论、证据引用和业务对象引用。

## 2. 入口与业务因果上下文

| 操作 | 触发入口 | 产出事件 |
| --- | --- | --- |
| `model.chat.completions` | OpenAI 兼容模型调用 | `model.call.observed` |

除 `traceparent`、`bkn-request-id`、账号与业务域头外，上游必须传入：

- `bkn-interaction-id`：当前交互或后台业务任务。
- `bkn-operation-id`：当前逻辑操作；同一次重试保持不变。
- `bkn-causation-event-id`：直接导致本次模型调用的上游事件。
- `bkn-attempt`：可选，默认 `1`。

缺少任一业务因果字段时不伪造业务事件，只保留既有技术 trace/log。合法的 W3C `traceparent` 才会复用；缺失或非法时为技术可观测生成本地 trace/span 标识。

## 3. 事件合同

一次成功或失败的调用只产生一个 `model.call.observed`。payload 精确允许：

```text
model_name
model_provider
status
input_token_count
output_token_count
prompt_hash
output_hash
error_category（仅错误时必填）
error_hash（仅错误时必填）
```

- `status` 仅为 `ok` 或 `error`。
- 所有哈希为 `sha256:<64 位小写十六进制>`。
- `event_id` 由 `trace_id + operation_id + event_type + attempt` 确定，同一次投递和重试投递保持稳定。
- 事件异步、失败开放提交；BKN Trace 不可用不得改变模型 API 的业务响应。

## 4. 敏感数据边界

禁止进入事件、普通日志和 span：API key、Authorization、Cookie、完整 prompt/messages、完整模型输出、工具 schema/参数/结果、供应商原始错误、PII。

prompt 和输出只记录哈希；模型参数只参与 prompt 哈希计算且仅纳入安全白名单。错误只记录稳定类别和错误摘要哈希，不记录错误原文。

## 5. Fixture 与 Given-When-Then

正向 fixture：`fixtures/bkn-trace/phase2/mf_model_call_l2_positive.json`。

- Given 合法技术上下文和完整业务因果上下文，When 模型调用成功，Then 产出一个与相同 trace、request、interaction、operation 关联的 `model.call.observed`。
- Given prompt、输出和工具声明含业务数据或个人信息，When 构造事件，Then 事件仅包含哈希、计数和安全枚举。
- Given 同一 operation 与 attempt 被重复投递，When 重建事件，Then `event_id` 保持一致。
- Given 缺少业务因果上下文，When 模型调用结束，Then 不创建不可解释的孤立业务事件。
- Given BKN Trace 未配置或提交失败，When 模型调用结束，Then 模型 API 响应不受影响。

## 6. 当前边界

- 当前首先覆盖 OpenAI 兼容调用路径；其他模型适配器接入时必须遵循同一合同。
- prompt 与输出快照不由模型模块保存；需要快照时由上层业务主体按权限和保留策略显式创建证据快照。
- 结论、证据引用、业务对象/属性/关系引用和行动状态由 Agent 或 AI 应用基于多个来源事件统一生成。

## 7. 责任确认

- 责任方：OpenBKN Foundry / Model Factory。
- 评审日期：待定。
- 兼容风险：低；事件提交默认关闭，开启后仍为失败开放。
