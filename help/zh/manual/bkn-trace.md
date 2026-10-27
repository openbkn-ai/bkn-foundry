# 🔭 BKN Trace

## 📖 概述

**BKN Trace** 提供**全链路可观测**：接收 OTLP 链路、导出到检索后端，并通过 **agent-observability** 服务查询与智能体及平台活动关联的 Span。每一次智能体对话都会生成完整的执行链路，记录从用户输入到最终回复的每一步决策与操作。

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/agent-observability/v1` | 链路查询与可观测 API |

**相关模块：** 平台日志、指标管道（如 feed-ingester）。

**升级：** 从 0.1.3 升级到 0.1.4 且需要清理不兼容的历史 Trace 数据时，参见[生产升级与历史数据清理](bkn-trace-0.1.3-to-0.1.4-upgrade.md)。

## Community 与 EE 接口边界

Community 提供受授权的基础事实和技术 Trace 读取。企业原生业务溯源需要目标
Trace 服务实际装配 EE 实现，并通过运行时许可及当前身份的记录授权。
路径前缀均为 `/api/agent-observability/v1`：

| 接口 | 范围 |
| --- | --- |
| `GET /interactions/{id}`、`GET /interactions/{id}/operations` | Core 已记录事实 |
| `GET /interactions/{id}/business-graph` | Core 已记录组装修订的授权投影，不等于 EE 原生详情或解释 |
| `GET /traces`、`GET /traces/{trace_id}` | Core 技术执行读取 |
| `GET /business-provenance/conversations`、`GET /business-provenance/interactions` | EE 业务检索 |
| `GET /business-provenance/interactions/{id}` | EE 原生业务解析详情 |
| `GET /business-provenance/interactions/{id}/explanations` | EE 已有解释，只读；返回 `ready` 或 `not_generated` |

Community 不挂载这些 EE 路由，0.2.0 已有此边界。普通 404 不能单独判断为记录不存在、
缺许可证或网关错误：EE 许可门也可能返回同样的普通 404。Core 标准读取的结构化
`resource_not_disclosed` 则表示当前范围不可披露；基础读取 200 不证明企业入口可用。

不要用其他 EE 服务、证书或角色推断目标 Trace 镜像已包含企业实现。客户端应保留
已授权的基础事实，对企业视图如实显示不可用/未披露。解释 GET 不生成内容；显式生成
使用独立的 EE POST，不能在打开面板或可用性探测时隐式调用。非空企业验收需要已有
真实记录和实际授权；`completed` 或 Operations 非空不能替代 `ready` 解释验收。

旧 trace/request Evidence Chain、Business Graph、Evidence Node 路径不属于当前挂载的
企业读取合同。完整边界与排查说明见[服务 README](../../../bkn-trace/agent-observability/README.md#community-与-ee-读取边界2517)。

## Interaction 自动完成链路的限制

启用可靠答案交接后，Core 在同一 SQL 事务中保存答案任务并结束业务。
`execution_status=completed` 允许会话开始下一轮；`evidence_status=assembling`
表示证据仍待投递。响应丢失不代表已确认完成，应幂等重试原 finish，不能重新执行业务工具。

MCP start 的问题 artifact 保留同步、尽力采集路径，其 I/O 与生命周期调用共享现有
1 秒 Trace pre 预算；单次上传期限和重试不会各自重新获得一份 pre 预算。
start 提交后若采集失败，响应保留权威 Interaction ID，并说明
`lifecycle_recorded=true`、证据 `recorded=false`，指引为
`continue_business_without_trace_retry`。artifact 端点故障使用
`evidence_store_unavailable`，表示采集路径不可用，不断言底层存储的故障原因；
生命周期 Core 故障仍用 `trace_core_unavailable`。明确的采集认证配置缺失属于运维问题，
不重复重试。不伪造 artifact 引用，问题采集路径也不承诺可靠后台恢复。

### Interaction finish 的权威确认

finish 准备工作保留现有 1 秒观察预算，最终终态 POST／幂等重放另用默认 5000 毫秒
总预算，配置为 `observability.lifecycle.finish_timeout_ms` 或
`BKN_TRACE_INTERACTION_FINISH_TIMEOUT_MS`（1–30000 毫秒，无效值使用 5000）。
最多 3 次完全相同的 POST，包括退避，共享这份预算。授权准备完成后，调用方断开时
仍可在此有限预算内收尾；工具 attempt 的回执预算及 Core 的 Interaction 租约规则不变。

只有 Core 返回匹配的 Interaction ID 和所请求终态，finish 才确认成功。
超时、临时故障重试耗尽或无效 ACK 返回 MCP `isError=true`、
`interaction_finish_unconfirmed`、`required_action=retry_same_finish`，并附阶段、
原因码和 request 关联信息。不能仅看 HTTP 200 判断已收尾。
应使用相同 Interaction、outcome、answer 重试原 finish，不重跑业务动作。
重复同一 POST 由 Core 验证已提交闭包，不把单纯 GET 到 completed 当成接受了另一份答案。
已 abandoned 等领域拒绝仍返回错误，不强行覆盖终态。

不新增持久化客户端重试队列。Core 持续故障或进程退出时，未被接受的 finish 仍可能
保持 active，直至租约回收；不承诺未获 Core 接受就必定完成，也不伪造终态。
本修复不把历史 8081 超时归因为某个尚未证实的存储或 CPU 问题。

### 调度与时间预算

每个 Core 实例使用一个共享的投影 worker 执行槽，空闲时认领一条就绪任务。
队列非空时连续消费，空队列或 worker 出错后才按配置间隔再检查；默认 1 秒轮询
不等于每秒只能处理一条。慢答案仍会延迟该槽位后续的证据任务；进入退避的重试不占槽位。
其它投影 handler 保留既有行为。
答案认领只提交元数据和租约，不读取正文；正文随后在同一个答案任务期限内读取，
并校验当前租约。正文读取超时沿用计数重试和退避，让后续就绪任务继续处理；
尝试耗尽后进入既有死信流程。

| 配置 | 默认值 | 含义 |
| --- | --- | --- |
| `core.projection.leaseIOTimeout` | `2s` | SQL 元数据／租约认领 I/O 期限 |
| `core.projection.answerTaskIOTimeout` | `10s` | 一次答案处理，包括 SQL 正文读取、顺序存储请求和 SQL 结算 |
| `core.projection.answerAckIOTimeout` | `2s` | 独立记录结果、重试或死信的期限 |
| `core.projection.lockDuration` | `30s` | outbox 任务租约，与业务 Interaction 租约不同 |

各值必须为正；认领＋答案处理＋ACK＋1 秒安全余量必须严格小于租约，否则拒绝启动。
默认总预算为 15 秒，留在 30 秒租约内。单纯延长租约不会提高吞吐，反而延迟崩溃后的恢复。
这些是 I/O 期限，不是 CPU、数据库或存储高负载下的端到端耗时保证；
不会重新定义共享 worker 中企业版或 revision-input handler 的运行时限。
其它已注册 handler 如需更长时间，也必须与共享租约匹配；本次不增加租约续期机制。
显式配置的启动投影重建完成后，才进入正常 worker 消费。
业务 Interaction 的既有租约和续租规则不变；后台证据投递不会让已完成的 Interaction 保持 active。

### 大小与准入

- 完整编码后的答案 artifact 最多 **16 MiB**，包含元数据，不等于纯文本可以写满
  16 MiB；外层 owner／任务包装也占空间。任务上限为 16 MiB＋64 KiB。
  managed finish 请求总包体最多 17 MiB，关闭新准入后仍保留该解析上限，以支持已提交
  完整正文的幂等重放；其它生命周期接口继续使用原有 2 MiB 包体上限。
- Core 在实际插入事务中，按编码后的任务及协议余量校验 `max_allowed_packet`。
  数据库设为 16 MiB 不代表可以容纳完整 16 MiB artifact 加包装。
  预算不足时返回不可重试的存储限制错误并回滚 finish；由运维确认并解决限制后，
  再重试原 finish，产品不会静默截断答案。
  当前 SQL 校验为协议和固定值预留 4 KiB，另计任务元数据；最终有效上限取各项预算的最小值。
- 可靠交接要求使用默认的 prepared statement 路径；Core DSN 中的
  `interpolateParams` 应省略或设为 false。客户端 SQL 插值会因转义改变传输大小，
  与可靠交接同时启用时拒绝启动。
- 新采集必须由已知且允许的服务端策略准入。策略关闭时沿用服务端关闭原因，
  不暂存新正文；策略不可确认不能视为关闭。已提交请求的幂等重放、已接收任务的
  后台恢复，不作为一次新的采集准入。
  可靠交接开关未开启时，新答案任务会返回运维配置指引；修改答案或自动重试不能启用该功能。

### 组装、积压与恢复

默认组装截止时间为 **5 分钟**。到期后由周期检查标记证据缺失，不代表证据保证在
5 分钟内完整；迟到内容到达后可生成后续证据 revision，不改写已完成的业务结果。

自动重试默认最多 10 次尝试，并设有 24 小时重试年龄边界。两者都不是保留期限：
死信正文继续保留，租约重领也不等于一次已计数的投递失败。已接收任务可在进程重启后恢复。
只有 artifact 已确认保存、SQL 证据状态结算后，成功 ACK 才释放 outbox 的答案正文；
待处理、失败和死信正文保留供恢复，已交付答案由 artifact 存储持有。

当前没有全局待投递字节硬配额，也没有认证固定 QPS。未交付正文的空间按
“到达速率 × 平均编码任务大小 × 故障时长”预算，死信复制可使这部分正文空间约翻倍；
另外预留既有数据、undo、索引及日志空间。例如平均 1 MiB、每秒一条，停止交付
1 小时会积压约 3.52 GiB 正文。等待时间与容量还受共享 SQL、worker 中其它任务影响。
单任务／认领字节上限不构成进程总内存或请求并发上限。
共享 CPU、SQL 与存储压力也可能影响列表／详情响应时间。

两个可靠答案开关默认关闭。先升级全部 Core worker，再启用 Core、Context Loader。
回退 Core 程序前需核对已接收任务，保留兼容 worker 和数据库包大小配置。
不能为补证据而重新执行已完成的业务。

## 🧱 Trace 数据结构

一次智能体对话产生的 Trace 由多个 **Span** 组成，形成父子树形结构：

```
conversation (根 Span)
├── planning                     → LLM 规划阶段
│   ├── context_retrieval        → 上下文检索
│   │   ├── kn_search            → 知识网络搜索
│   │   └── instance_query       → 实例查询
│   └── llm_call                 → 模型调用
├── tool_execution               → 工具执行阶段
│   ├── tool:web_search          → Web 搜索工具
│   └── tool:code_runner         → 代码执行工具
├── synthesis                    → 结果合成
│   └── llm_call                 → 模型调用（生成回复）
└── response                     → 最终响应
```

### Span 关键字段

| 字段 | 说明 |
| --- | --- |
| `span_id` | Span 唯一标识 |
| `parent_span_id` | 父 Span ID（根 Span 为空） |
| `operation` | 操作名称（如 `planning`、`tool:web_search`） |
| `start_time` / `end_time` | 起止时间戳（ISO 8601） |
| `duration_ms` | 耗时（毫秒） |
| `status` | 状态：`ok`、`error`、`timeout` |
| `attributes` | 键值属性（如 `llm.model`、`tool.name`、`query`） |
| `events` | 事件列表（如 `token_usage`、`error`） |

### 证据链分析

对于结果证据缺失，`current_record_integrity.missing[]` 可包含从已记录发布结果提取的
`drop_reason`。`publisher_unavailable` 表示发布器无法接收结果事件，`queue_full` 表示
发布队列已满。业务调用可以是 `completed`，同时结果证据仍然缺失。没有 `drop_reason`
表示未记录适用于该结果事件的发布失败原因。

ontology-query 明确启用证据发布器时，无效的启动配置会阻止服务启动，并指出具体配置项。
临时的策略读取、心跳或 ACK 失败沿用现有后台重试；运行日志指出 producer 和失败环节，
相同错误连续发生时只记录一次，恢复后记录恢复信息。

Trace 数据支持以下分析场景：

- **性能瓶颈定位**：通过 `duration_ms` 找到耗时最长的 Span
- **错误溯源**：从 `status: error` 的 Span 追溯到根因
- **Token 用量审计**：通过 `events` 中的 `token_usage` 事件统计模型调用成本
- **决策路径回放**：按父子关系重建智能体的完整思考与执行路径
- **工具调用审计**：检查每个工具调用的输入参数与返回结果

### CLI

`openbkn agent trace` 需要两个参数：**智能体 ID**、**会话 ID**（与 `openbkn agent sessions <agent_id>` 或对话返回一致）。

#### 查看对话链路

```bash
# 格式化输出 — 以树形结构展示 Span 层级、耗时与状态
openbkn agent trace agt_001 conv_20250115_001 --pretty
```

`--pretty` 输出示例：

```
Trace: conv_20250115_001
总耗时: 3,245ms | Span 数: 12 | 状态: ok

conversation (3,245ms) ✓
├── planning (1,820ms) ✓
│   ├── context_retrieval (1,200ms) ✓
│   │   ├── kn_search (450ms) ✓
│   │   │   query: "近期高价值客户"
│   │   │   results: 8
│   │   └── instance_query (720ms) ✓
│   │       object_type: ot_customer
│   │       conditions: status == active
│   │       results: 23
│   └── llm_call (580ms) ✓
│       model: gpt-4o
│       tokens: 1,240 (prompt) + 320 (completion)
├── tool_execution (890ms) ✓
│   └── tool:code_runner (890ms) ✓
│       code: "df.groupby('region').sum()"
│       exit_code: 0
├── synthesis (480ms) ✓
│   └── llm_call (480ms) ✓
│       model: gpt-4o
│       tokens: 2,100 (prompt) + 560 (completion)
└── response (55ms) ✓
    content_length: 1,240 chars
```

```bash
# 紧凑输出 — 适合管道处理与日志聚合
openbkn agent trace agt_001 conv_20250115_001 --compact
```

`--compact` 输出为单行 JSON 数组，每个元素一个 Span：

```json
[
  {"span_id":"sp_001","parent":"","op":"conversation","duration_ms":3245,"status":"ok"},
  {"span_id":"sp_002","parent":"sp_001","op":"planning","duration_ms":1820,"status":"ok"},
  {"span_id":"sp_003","parent":"sp_002","op":"context_retrieval","duration_ms":1200,"status":"ok"}
]
```

#### 解读 Trace 输出

**时序分析**：比较同层级 Span 的 `duration_ms`，定位瓶颈。上例中 `context_retrieval` 占 `planning` 的 66%，可考虑优化知识网络索引。

**父子关系**：`parent_span_id` 为空表示根 Span。每个子 Span 的时间区间应在父 Span 内。多个子 Span 可能并行执行（时间区间重叠）或顺序执行。

**错误追踪**：当某个 Span 的 `status` 为 `error` 时，检查其 `events` 列表中的 `error` 事件，包含 `message` 与 `stack_trace`。从失败的叶子节点向根回溯，可还原完整的错误传播路径。

---

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

// 拉取某次对话的全部 Span
const spans = await bkn.trace.spans('conv-xyz789');

// 遍历 Span
for (const span of spans) {
  const indent = span.parentSpanId ? '  ' : '';
  console.log(
    `${indent}${span.name} (${span.type}) — ${span.durationMs}ms — ${span.status}`,
  );

  if (span.type === 'retrieval') {
    console.log(`    query: ${span.attributes.query}`);
    console.log(`    results: ${span.attributes.resultsCount}`);
    console.log(`    sources: ${span.attributes.sources}`);
  } else if (span.type === 'tool') {
    console.log(`    tool: ${span.attributes.toolId}`);
    console.log(`    input:`, span.attributes.input);
    console.log(`    output:`, span.attributes.output);
  } else if (span.type === 'llm') {
    console.log(`    model: ${span.attributes.model}`);
    console.log(
      `    tokens: ${span.attributes.promptTokens}+${span.attributes.completionTokens}`,
    );
  }
}

// 证据链分析
const retrievalSpans = spans.filter((s) => s.type === 'retrieval');
const toolSpans = spans.filter((s) => s.type === 'tool');

console.log('\n--- 证据来源 ---');
retrievalSpans.forEach((rs) => {
  console.log(`知识网络: ${rs.attributes.knId}`);
  console.log(`  对象类: ${rs.attributes.sources}`);
});
toolSpans.forEach((ts) => {
  console.log(`工具: ${ts.attributes.toolId}`);
  console.log(`  输出:`, ts.attributes.output);
});
```

---

### curl

```bash
# 获取对话 Trace
curl -sk "https://<访问地址>/api/agent-observability/v1/traces/conv_20250115_001" \
  -H "Authorization: Bearer $(openbkn token)"

# 按条件搜索 Trace
curl -sk -X POST "https://<访问地址>/api/agent-observability/v1/traces/search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "agent_id": "agt_001",
    "start_time": "2025-01-15T00:00:00Z",
    "end_time": "2025-01-16T00:00:00Z",
    "status": "error",
    "limit": 20
  }'

# 获取特定 Span 详情
curl -sk "https://<访问地址>/api/agent-observability/v1/spans/sp_003" \
  -H "Authorization: Bearer $(openbkn token)"

# 获取 Trace 统计摘要（耗时分布、错误率等）
curl -sk "https://<访问地址>/api/agent-observability/v1/traces/conv_20250115_001/summary" \
  -H "Authorization: Bearer $(openbkn token)"
```
