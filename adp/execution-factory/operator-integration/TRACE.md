# operator-integration Trace Contract

> 状态：阶段一模块接入合同
> 适用版本：`bkn.trace.schema.version=1.0.0`
> 依据：`bkn-docs/docs/foundry/bkn-trace/design/阶段一：OpenBKN 可观测记录规范与 Trace Context 基线.md`

## Module

- module name: `action-execution`
- observed service: `operator-integration`
- owner: OpenBKN Foundry / execution-factory
- service identity: `agent-operator-integration`
- runtime: Go HTTP / MCP / toolbox / sandbox execution service
- repository path: `adp/execution-factory/operator-integration`
- contract version: `1.0.0`

## Entry Operations

| operation | trigger | required context | emitted spans | emitted events |
| --- | --- | --- | --- | --- |
| `action.recommend` | action/tool recommendation created by upstream agent or workflow | `traceparent`、`bkn-request-id`、actor/policy context | `action-execution.request` | `action.recommended` |
| `action.approve` | policy or user approval check | `traceparent`、`bkn-request-id`、actor/policy context | `action-execution.policy.check` | `action.approved` / `policy.denied` |
| `action.execute` | operator proxy、MCP tool call、toolbox execution、function sandbox execution | `traceparent`、`bkn-request-id`、actor/action refs | `action-execution.request`、`action-execution.invoke` | `action.executed` / `action.failed` |
| `action.result` | execution result returned or recorded | `traceparent`、`bkn-request-id`、action invocation refs | `action-execution.invoke` | `action.result_recorded` |

## Inbound Context

- accepted headers / metadata: `traceparent`、`bkn-request-id`、legacy `x-request-id`、`baggage`、`x-account-id`、`x-account-type`、`x-business-domain`、`user_id`。
- `traceparent` parsing: HTTP trace middleware extracts W3C Trace Context before `middlewareTraceContext` stores OpenBKN request context.
- invalid context handling: invalid or missing request id is replaced by generated `req_<uuid>` value.
- request id generation: `common.SetTraceContextToCtx` generates request id when inbound `bkn-request-id` and `x-request-id` are missing or invalid.
- tenant/account/auth context source: public endpoints use token introspection / app key verification; internal endpoints use account headers.
- baggage policy: only `bkn.account.type` and `bkn.runtime.env` are retained; actor id、account id、tenant/domain id、tool args、prompt、SQL、execution payload must not be propagated through baggage.

## Outbound Calls

| target | protocol | propagated fields | baggage policy | timeout | retry |
| --- | --- | --- | --- | --- | --- |
| toolbox / imported tool target | HTTP | `traceparent`、`bkn-request-id`、`x-request-id`、account headers | allowlist only | existing proxy timeout | existing proxy policy |
| MCP server | MCP / in-process / HTTP transport | request context on `context.Context` and supported metadata | allowlist only | existing MCP timeout | existing MCP policy |
| sandbox control plane | HTTP / internal client | `traceparent`、`bkn-request-id`、account headers | allowlist only | execution timeout | sandbox policy |
| authorization / bkn-safe | HTTP | `traceparent`、`bkn-request-id`、account headers | no raw actor identity in baggage | existing client timeout | existing client policy |

Allowed baggage fields:

```text
bkn.account.type
bkn.runtime.env
```

## Logs

| log type | level | required fields | indexed fields | sensitive fields | example fixture |
| --- | --- | --- | --- | --- | --- |
| business | info | `trace_id`、`span_id`、`bkn.request.id`、`bkn.module.name`、`bkn.operation.name`、`bkn.status` | module、operation、status、action type、tool/operator id | complete tool args/result、sandbox stdout/stderr | `fixtures/bkn-trace/positive.json` |
| error | error | business fields + `error.category`、`error.code`、`error.retryable` | category、code、retryable | raw dependency response、external payload | `fixtures/bkn-trace/sampling.json` |
| audit | info | actor、policy、decision、resource ref、operation | decision、resource class | raw approval note、credential、target payload | `fixtures/bkn-trace/propagation.json` |

## Spans

| span name | kind | required attributes | parent/link rule | error mapping |
| --- | --- | --- | --- | --- |
| `action-execution.request` | server | module、operation、status、request id、action/tool/operator ref | HTTP entry span | validation/authz/dependency/tool |
| `action-execution.policy.check` | internal/client | actor、policy decision、resource ref、status | child of request span | denied maps to `bkn.status=denied` |
| `action-execution.invoke` | internal/client | action type、invocation ref、tool/operator id、status、duration | child/link from request span | dependency/tool/timeout |

## Events

| event type | producer | payload summary | partial reason | retention class |
| --- | --- | --- | --- | --- |
| `action.recommended` | upstream agent / operator-integration | action type、recommendation hash、policy context | recommendation source partial | forced retention |
| `action.approved` | operator-integration / policy service | actor ref、policy id、decision ref | policy service unavailable | audit |
| `policy.denied` | operator-integration / policy service | actor ref、resource ref、reason code | redacted reason | forced retention |
| `action.executed` | operator-integration | action invocation id、tool/operator ref、status | async result pending | forced retention |
| `action.failed` | operator-integration | action invocation id、error code、retryable | dependency timeout / validation failed | forced retention |
| `action.result_recorded` | operator-integration | action invocation id、result hash、classification | result redacted/truncated | business event |

## Business Refs

| ref type | field | resolver | version field | visibility rule |
| --- | --- | --- | --- | --- |
| action type | `bkn.action_type.id` | BKN/ontology resolver | schema version | account/domain policy |
| invocation | `bkn.action_invocation.id` | action execution resolver | invocation version / created_at | actor/policy visibility |
| actor | `bkn.actor.id` | auth / bkn-safe resolver | token/session version | audit policy |
| policy | `policy.id` / `bkn.auth.decision` | bkn-safe resolver | policy version | audit policy |
| tool/operator | `bkn.tool.id` / `operator_id` | execution-factory resolver | release version | account/domain policy |

## Sensitive Data Rules

- never log: token、authorization、cookie、执行凭据、完整工具输入输出、完整函数代码、完整 stdout/stderr、完整外部响应、未脱敏审批备注、目标系统敏感 payload。
- hash only: action input、tool args、tool result、sandbox stdout/stderr、external response summary。
- controlled reference: action invocation、large execution artifact、approval evidence、external result artifact。
- redact: unauthorized action detail、PII、secret connection metadata、credential-like values。
- current code baseline: HTTP API logs record request body length/hash instead of raw body; function execution logs record stdout/stderr length/hash instead of raw output.

## Sampling

- default: normal successful read/metadata operations can follow platform sampling policy.
- forced sampling: `error`、`timeout`、`denied`、`security/audit`、`action.recommended`、`action.approved`、`action.executed`、`action.failed`、`action.result_recorded`。
- not sampled behavior: keep required business log and dropped counters.
- dropped counters: S3 follow-up.

## Fixtures

| fixture | path | purpose | expected result |
| --- | --- | --- | --- |
| positive | `fixtures/bkn-trace/positive.json` | recommendation baseline | pass |
| negative | `fixtures/bkn-trace/negative_baggage.json` | forbidden actor id in baggage | fail |
| propagation | `fixtures/bkn-trace/propagation.json` | execute keeps request context | pass |
| sampling | `fixtures/bkn-trace/sampling.json` | policy denied forced sampled | pass |

## Covered GWT

- GWT-01 无上游上下文。
- GWT-02 可信上游 Trace Context。
- GWT-05 baggage 违规。
- GWT-07 后台/异步执行关联。
- GWT-08 工具或依赖失败。
- GWT-09 权限拒绝。
- GWT-10 敏感数据扫描。
- GWT-12 强制保留。
- GWT-13 字段索引分层。

## Known Gaps

- runtime `action.recommended/action.approved/action.executed/action.result_recorded` event emitters are not complete in this branch.
- action invocation id is not yet normalized across operator proxy、MCP tool call、toolbox execution and sandbox execution.
- policy decision events currently rely on existing audit log path; a dedicated BKN Trace event emitter is a follow-up.
- full registry validation、indexing policy validation and S3 health metrics rely on bkn-docs validator/S3 follow-up.

## Owner Sign-off

- owner: OpenBKN Foundry / execution-factory
- reviewed at: 2026-07-21
- reviewer: pending
- compatibility risk: low; new headers are additive and legacy `x-request-id` remains supported.
