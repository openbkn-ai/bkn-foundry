# operator-integration BKN Trace 接入规范

> 状态：2.1 Action 因果闭环实施基线
> 更新时间：2026-07-25
> 依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 1. 模块职责

- 模块名：`action-execution`；观测服务：`operator-integration`。
- 运行形态：Go HTTP/MCP/toolbox/sandbox 执行服务。
- 代码路径：`adp/execution-factory/operator-integration`。
- Trace span 使用 `1.0.0`，Evidence event 默认写入 `2.1.0`。
- 本模块只陈述权限决策、执行尝试和执行结果，不代替 Agent 生成行动建议或审批申请。

## 2. Action 事实责任

| 事实 | 责任方 | 本模块行为 |
| --- | --- | --- |
| `action.recommended` | Agent、AI 应用或工作流 | 不生成；要求上游先写入 |
| `action.approval_requested` | Agent、AI 应用或工作流 | 不生成；接收入站事件 ID 作为直接原因 |
| `action.approved` / `action.rejected` | operator-integration 的真实权限边界 | 权限检查后生成 |
| `action.executed` | operator-integration | 实际开始执行或执行尝试失败时生成 |
| `action.result_recorded` | operator-integration | 记录结果哈希及受控 task/artifact ref |

上游必须先可靠提交 `recommended -> approval_requested`，再调用执行入口。execution 不根据工具参数猜测建议，不补造缺失的上游状态。

## 3. 入口与上下文

Action 执行复用真实 toolbox `ExecuteTool` 权限与执行边界。除已有身份头外，必须携带：

```text
traceparent
bkn-request-id
bkn-interaction-id
bkn-operation-id
bkn-causation-event-id
bkn-claim-id
bkn-attempt
bkn-action-instance-id
bkn-action-type
bkn-action-reversible
bkn-action-policy-ref
bkn-action-observed-at
bkn-action-approval-requested-event-id
x-account-id
x-account-type
x-business-domain（缺失时暂以 account id 代替）
```

- `bkn-action-approval-requested-event-id` 是 `approved/rejected` 的直接原因。
- `bkn-operation-id` 在重试时保持不变，`bkn-attempt` 递增且进入 event ID。
- 缺任一必要因果字段时保持原执行行为，但不创建孤立 Action 事件。
- 当前自动测试策略只接受 `action_type=monitor`、`reversible=true`、`policy_ref=e2e-monitor-auto-approve`。
- 其他 Action 不得被自动批准，也不得被普通工具调用误判为 Action。

## 4. 状态机与幂等

本模块仅接续以下状态：

```text
approval_requested -> approved | rejected
approved -> executed
executed -> result_recorded
```

- `rejected` 是终态，拒绝后不得访问工具箱数据库、元数据或执行代理。
- 权限通过但后续依赖失败时仍记录 `approved -> executed(error) -> result_recorded(error)`。
- event ID 由 `action_instance_id + operation_id + attempt + event_type` 稳定派生。
- 同一 attempt 重放相同事件内容必须完全一致；不同 attempt 不得复用 event ID。

## 5. 精确 payload

| 事件 | 允许字段 |
| --- | --- |
| `action.approved` / `action.rejected` | `action_instance_id`、`actor_ref`、`policy_decision_ref`、`status` |
| `action.executed` | `action_instance_id`、`invocation_ref` 或 `tool_ref`、`status`、错误时的 `error_category/error_hash` |
| `action.result_recorded` | `action_instance_id`、`result_hash`、`artifact_ref` 或 `task_ref`、`status` |

所有哈希使用 `sha256:<64 位小写十六进制>`。actor、policy decision、tool、task 均使用不可逆受控引用；不得保存原始用户 ID、工具 ID 或审批意见。

## 6. Trace 与传播

| 调用目标 | 协议 | 传播字段 | 约束 |
| --- | --- | --- | --- |
| toolbox/导入工具 | HTTP | trace、request、account 及受控 Action 上下文 | 不传播原始参数和结果 |
| MCP server | MCP/HTTP | 当前 `context.Context` 支持的 trace 元数据 | baggage 仅 allowlist |
| sandbox control plane | HTTP | trace、request、account | 使用既有执行超时 |
| authorization/bkn-safe | HTTP | trace、request、account | baggage 不放 actor 原始标识 |

允许的 baggage 仅为 `bkn.account.type`、`bkn.runtime.env`。

## 7. 敏感数据边界

禁止进入 event、普通日志、span 和 Studio 响应：token、Authorization、Cookie、执行凭据、完整工具输入/输出、完整函数代码、stdout/stderr、外部响应、审批意见、SQL、PII、目标系统敏感 payload。

允许记录：安全枚举、长度/计数、完整 SHA-256、受控 action/tool/task/artifact/policy 引用。错误只记录类别和哈希，不记录错误原文。

## 8. Given-When-Then 验收

- Given 上游已提交 recommendation 和 approval request，When 权限通过且执行成功，Then 仅新增 approved、executed、result_recorded。
- Given 权限拒绝，When 进入真实权限边界，Then 仅新增 rejected，且不访问执行依赖。
- Given 执行失败，When 权限已通过，Then executed/result 的 status 为 error，错误原文不可见。
- Given 同一 operation/attempt 重放，When 重建事件，Then event ID 与完整内容一致。
- Given 缺 Action 因果头或不是可撤销 monitor 测试策略，When 调用工具，Then 不生成 Action 事件。

## 9. Fixture 与测试

- 正向 fixture：`fixtures/bkn-trace/action_2_1_positive.json`。
- 合同测试：`server/infra/bkntrace/evidence_test.go`。
- 执行边界测试：`server/logics/toolbox/execute_trace_test.go`。
- 验证命令：`go test ./server/infra/bkntrace ./server/logics/toolbox`。

## 10. 当前边界

- 本批先接入 toolbox `ExecuteTool`；MCP、operator proxy 和 sandbox 后续复用同一 helper。
- `BKN_TRACE_EVIDENCE_INGEST_URL` 为空或写入失败时 fail-open，不改变业务响应。
- 上游 recommendation/approval request 的可靠写入顺序由调用方或 SDK `flush` 保证。
- 完整本地环境必须把 ingest URL 指向 agent-observability 真实地址。

## 11. 责任确认

- 责任方：OpenBKN Foundry / execution-factory。
- 评审日期：待定。
- 兼容风险：中；新增头均为可选，但只有完整 2.1 上下文才启用 Action 证据事件。
