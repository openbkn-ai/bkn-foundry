# bkn-backend BKN Trace 接入合同

> 状态：BKN Trace 2.1 生产者实施基线
> 更新时间：2026-07-26
> 权威依据：`bkn-docs/docs/foundry/bkn-trace/registry/核心业务事件注册表.md`

## 一、模块责任

- 模块名：`bkn-backend`。
- 观测对象：对象类型、属性、关系类型、行动类型和指标定义等知识网络 schema 读取。
- 事实事件：`knowledge.read.observed`。
- 本模块只记录读取事实，不创建 `claim.created`、`evidence.refs.created` 或 `business.refs.resolved`；Agent/AI 应用形成结论后负责引用事实事件并完成结论绑定。

## 二、上下文与重放合同

入站事实生产要求合法 `traceparent`、request/account 上下文，以及：

```text
bkn-interaction-id
bkn-operation-id
bkn-attempt
bkn-event-observed-at
bkn-causation-event-id（存在直接原因时）
```

- ID 只校验安全长度和字符，不强制 `int_/op_/evt_/claim_` 前缀。
- `attempt` 默认 `1`；同一逻辑操作重试保持 `operation_id` 不变并递增 `attempt`。
- `bkn-event-observed-at` 必须是调用方首次创建完整 envelope 时确定并复用的 UTC RFC3339Nano 时间。
- 受信服务入口在缺少 `bkn.request.id`、`interaction_id`、`operation_id` 或 `bkn-event-observed-at` 时，为当前根请求生成合法值，通过响应头回传并向下游传播；调用方提交的合法值保持不变。入口不得补造 `causation_event_id`、`claim_id` 或证据采用关系。
- 生产者在脱离受信入口、且上下文仍缺少或非法时不生成核心事实事件，避免同一事件 ID 搭配新时间戳造成冲突。
- `event_id = evt_ + sha256(trace_id|operation_id|event_type|attempt)` 的完整 64 位十六进制结果。
- 成功构造事实后通过响应头 `bkn-evidence-event-id` 返回真实事件 ID。

这能保证调用方持有并复用完整 envelope 时的跨进程重放。生产者不持久化 envelope，因此只有 `operation_id`、没有原始 `observed_at` 的进程重启重放不受支持，并会显式拒绝产出事件。

## 三、事实与引用

`knowledge.read.observed.payload` 精确使用：`kn_id`、`read_kind`、`version_status`、可选 `schema_version`、`business_refs`。

`business_refs` 在没有 claim 时也必须存在；按真实业务语义使用 `object/property/relation/metric/action`。关系类型不得映射为对象。Resolver 可解析的全限定 ID 为：

```text
object:<kn_id>:<object_type_id>
property:<kn_id>:<object_type_id>:<property_id>
relation:<kn_id>:<relation_id>
action_type:<kn_id>:<action_type_id>
metric:<kn_id>:<metric_id>
```

生产者只使用源实体明确携带的 `kn_id`；缺失时省略该引用，不从请求、账号或其他实体猜测。事件 `payload.kn_id` 与每个引用中的知识网络必须一致，否则过滤该引用。引用只允许 `ref_id/ref_type/source_system/validity/version_status/visibility/summary_hash（可选）`，不携带名称、注释、物理映射或 schema 原文。

## 四、可靠性与安全

- 上报最多尝试三次，HTTP 非 2xx 视为失败；队列满和最终失败必须记录可观测日志。
- 上报异步且失败开放，不改变业务响应。
- 禁止完整 SQL、参数、行数据、schema 原文、prompt、工具输入输出、依赖响应正文、数据库错误原文、整业务对象、完整 ID 列表、凭据、Cookie、token、连接串和对象存储裸 URL。
- 数据库与依赖错误只记录类型、SHA-256、长度和稳定操作名；SQL 只记录 hash、长度和参数数量；批量操作只记录请求数量和影响行数。
- `server/common` 的静态门禁扫描全部 driven adapter，拒绝直接原样错误日志和典型响应正文日志格式。
- 当前没有持久 outbox；进程退出可能丢失内存中事件，这是已知残余风险。

## 五、验收

- fixture：`fixtures/bkn-trace/phase2/bkn_schema_l2_positive.json`。
- Given 无 claim 且读取 schema，When 构造事实，Then 仍携带语义准确的 `business_refs`，且只产生一个读取事实。
- Given 关系类型，When 生成引用，Then `ref_type=relation`，不得为 `object`。
- Given 相同完整 envelope 重放，Then event ID 与时间戳均不变。
- Given 仅复用 operation 而缺少 `bkn-event-observed-at`，Then 不产生会与已有 ID 冲突的新事件。
- Given Studio 直接调用受信入口且未提交因果信封，Then 入口创建根请求信封、通过响应头回传，并成功产生可查询事实。
- Given 数据库错误或依赖返回包含 SQL、参数、行数据或 PII 的正文，Then 普通日志和 span 只保留安全摘要，不出现原文。

## 六、已知限制

schema 不可变版本和持久 outbox 尚未接入；当前引用统一为 `unversioned`。跨模块图组装、快照和展示由 BKN Trace 核心负责。
