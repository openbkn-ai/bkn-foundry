# 📚 Context Loader

## 📖 概述

**Context Loader** 实现 MCP（Model Context Protocol）JSON-RPC 协议的**分层检索**，为智能体组装高质量上下文。它在原始数据 / VEGA 与智能体运行时之间提供三层渐进式加载：

| 层级 | 内容 | 典型用途 |
| --- | --- | --- |
| Layer 1 | Schema 搜索 — 对象类、关系类元信息 | 理解领域结构 |
| Layer 2 | 实例查询 — 对象实例、子图 | 获取具体业务数据 |
| Layer 3 | 逻辑属性 & 动作信息 — 计算字段、可执行动作 | 驱动智能体决策与行动 |

典型 Ingress 前缀：

| 前缀 | 作用 |
| --- | --- |
| `/api/agent-retrieval/v1` | 检索与上下文组装 API |

**相关模块：** [BKN 引擎](bkn.md)、[VEGA 引擎](vega.md)。

---

## 🔌 MCP 接入

Context Loader 对外暴露标准 [MCP (Model Context Protocol)](https://modelcontextprotocol.io) 服务器，支持 Streamable HTTP 传输。AI 编码工具（Cursor、Claude Desktop、Cline 等）和自研 Agent 可直接通过 MCP 协议调用 Context Loader 的全部能力。

### 端点地址

```
https://<访问地址>/api/agent-retrieval/v1/mcp
```

### 在 Cursor 中配置

在项目根目录创建 `.cursor/mcp.json`（或全局 `~/.cursor/mcp.json`）：

```json
{
  "mcpServers": {
    "openbkn-context-loader": {
      "url": "https://<访问地址>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

Token 可通过 `openbkn auth token` 命令获取。配置保存后，Cursor 会自动发现 Context Loader 暴露的 MCP 工具，Agent 在对话中即可直接调用。

### 在 Claude Desktop 中配置

编辑 `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "openbkn-context-loader": {
      "url": "https://<访问地址>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

### MCP 工具列表

配置完成后，MCP 客户端可获取以下工具（以部署实际返回为准，用 `openbkn context info` 查看当前目录）：

| 工具 | 用途 |
|------|------|
| `search_schema` | 搜索对象类 / 关系类 / 行动类 / 指标的 Schema |
| `get_kn_detail` | 取知识网络 Schema，支持 `summary` 骨架与逐层下钻 |
| `get_object_types` / `get_relation_types` | 按 id 取对象类 / 关系类的完整定义 |
| `query_object_instance` | 条件查询对象实例 |
| `query_instance_subgraph` | 沿关系类路径查询实例子图 |
| `get_logic_properties_values` | 计算逻辑属性（派生字段）取值 |
| `get_action_info` | 取行动类信息、入参 Schema 与执行结果 Schema（`output_schema`） |
| `execute_action` | 执行行动类 |
| `get_action_execution` / `list_action_executions` | 查询行动执行状态与历史 |
| `find_skills` | 按对象类召回可用 Skill |
| `list_knowledge_networks` | 列出知识网络 |
| `list_resources` / `describe_resource` | 列出与描述 Vega 资源 |
| `run_sql` | 直接对资源执行 SQL |
| `bkn_start_interaction` / `bkn_finish_interaction` | 会话生命周期（业务可追溯性） |

每个工具调用需要 `kn_id`（知识网络 ID），可用 `openbkn bkn list` 获取。

### 使用 CLI 探测

不配置 MCP 客户端也能用 CLI 验证服务是否正常。CLI 没有「当前知识网络」这种全局配置，`kn-id` 是每条命令的位置参数：

```bash
# 查看部署的 MCP 工具目录（全局，不需要 kn-id）
openbkn context info

# 查看某个知识网络会话下实际公布的工具
openbkn context tools <kn-id>
```

---

## 💻 CLI

命令组是 `openbkn context`（早期的 `openbkn context-loader` 已改名，`config set/use/list/show/remove` 那套也一并取消——`kn-id` 现在是每条命令的位置参数）。取实例数据的几条统一用 `--args '<json>'` 传工具参数。

#### 目录与内省

```bash
# 部署级工具目录（不需要 kn-id）
openbkn context info

# 某个知识网络会话公布的工具 / 资源 / 提示词
openbkn context tools <kn-id>
openbkn context resources <kn-id>
openbkn context templates <kn-id>
openbkn context prompts <kn-id>
openbkn context prompt <kn-id> <name> --args '{"k":"v"}'
openbkn context resource <kn-id> <uri>
```

#### Schema 探索

```bash
# 语义搜索 Schema（可限定范围与返回上限）
openbkn context search-schema <kn-id> "客户订单关系"
openbkn context search-schema <kn-id> "哪些对象类描述了客户" --scope object,relation --max 10

# 渐进式取 Schema：先要骨架，再按 id 下钻
openbkn context kn-detail <kn-id> --detail-level summary
openbkn context object-types <kn-id> ot_customer ot_order
openbkn context relation-types <kn-id> rt_purchase
```

#### 实例查询

```bash
# 条件查询对象实例
openbkn context query-object-instance <kn-id> --args '{
  "ot_id": "ot_customer",
  "filters": [{"field": "status", "op": "==", "value": "active"}],
  "limit": 20
}'

# 沿关系类路径查询实例子图
openbkn context query-instance-subgraph <kn-id> --args '{
  "relation_type_paths": [{
    "object_types": [
      {"id": "ot_customer", "condition": {"operation": "and", "sub_conditions": []}, "limit": 10},
      {"id": "ot_order",    "condition": {"operation": "and", "sub_conditions": []}, "limit": 10}
    ],
    "relation_types": [{
      "relation_type_id": "rt_purchase",
      "source_object_type_id": "ot_customer",
      "target_object_type_id": "ot_order"
    }]
  }]
}'
```

#### 逻辑属性、行动与技能

```bash
# 计算逻辑属性取值
openbkn context get-logic-properties <kn-id> --args '{
  "ot_id": "ot_customer",
  "query": "这批客户近一年的终身价值与风险分",
  "_instance_identities": [{"...": "从 query_object_instance 返回的 _instance_identity 原样取"}],
  "properties": ["lifetime_value", "risk_score"]
}'

# 取某个行动类的工具定义与参数 Schema（at_id 必填，来自 search_schema / get_kn_detail）
openbkn context get-action-info <kn-id> --args '{
  "at_id": "at_send_coupon",
  "_instance_identities": [{"...": "从 query_object_instance 返回的 _instance_identity 原样取"}]
}'

# 按对象类召回可用 Skill
openbkn context find-skills <kn-id> ot_customer --top-k 5
```

#### 调用任意工具

工具目录会随版本增长，CLI 未必为每个工具都配了子命令。`tool-call` 按名调用任意 MCP 工具，`call-method` 调任意 MCP 方法：

```bash
openbkn context tool-call <kn-id> run_sql --args '{"sql":"SELECT 1"}'
openbkn context tool-call <kn-id> execute_action --args '{
  "at_id": "at_send_coupon",
  "_instance_identities": [{"...": "同上"}],
  "dynamic_params": {}
}'
openbkn context call-method <kn-id> tools/list
```

#### 端到端流程

```bash
KN=<kn-id>

# 1. 看有哪些工具可用
openbkn context tools "$KN"

# 2. Schema 探索 — 找到相关对象类
openbkn context search-schema "$KN" "客户" --scope object

# 3. 实例查询 — 取活跃客户
openbkn context query-object-instance "$KN" --args '{
  "ot_id": "ot_customer",
  "filters": [{"field": "status", "op": "==", "value": "active"}],
  "limit": 10
}'

# 4. 子图扩展 — 看该客户的购买关系
openbkn context query-instance-subgraph "$KN" --args '{
  "relation_type_paths": [{
    "object_types": [
      {"id": "ot_customer", "condition": {"operation": "and", "sub_conditions": []}, "limit": 10},
      {"id": "ot_order",    "condition": {"operation": "and", "sub_conditions": []}, "limit": 10}
    ],
    "relation_types": [{
      "relation_type_id": "rt_purchase",
      "source_object_type_id": "ot_customer",
      "target_object_type_id": "ot_order"
    }]
  }]
}'

# 5. 行动信息 — 看可对该客户执行什么
openbkn context get-action-info "$KN" --args '{
  "at_id": "at_send_coupon",
  "_instance_identities": [{"...": "取自第 3 步返回的 _instance_identity"}]
}'
```

---

### TypeScript SDK

> 💡 更多可运行示例见随 `@openbkn/bkn-sdk` 包发布的示例目录。

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

const knId = 'kn-001';

// 工具目录
console.log(await bkn.context.info());
console.log(await bkn.context.tools(knId));

// Schema 探索
const schema = await bkn.context.searchSchema(knId, '客户订单关系', { scope: ['object', 'relation'] });
const skeleton = await bkn.context.knDetail(knId, 'summary');
const ots = await bkn.context.objectTypes(knId, ['ot_customer']);

// 实例查询
const instances = await bkn.context.queryObjectInstance(knId, {
  ot_id: 'ot_customer',
  filters: [{ field: 'status', op: '==', value: 'active' }],
  limit: 20,
});

// 子图遍历
const subgraph = await bkn.context.queryInstanceSubgraph(knId, {
  relation_type_paths: [{
    object_types: [
      { id: 'ot_customer', condition: { operation: 'and', sub_conditions: [] }, limit: 10 },
      { id: 'ot_order', condition: { operation: 'and', sub_conditions: [] }, limit: 10 },
    ],
    relation_types: [{
      relation_type_id: 'rt_purchase',
      source_object_type_id: 'ot_customer',
      target_object_type_id: 'ot_order',
    }],
  }],
});

// 逻辑属性与行动
// _instance_identities 必须原样取自上一步返回的 _instance_identity，不能自己编
const logic = await bkn.context.logicProperties(knId, {
  ot_id: 'ot_customer',
  query: '这批客户近一年的终身价值',
  _instance_identities: instances.map((r: any) => r._instance_identity),
  properties: ['lifetime_value'],
});
const actions = await bkn.context.actionInfo(knId, {
  at_id: 'at_send_coupon',
  _instance_identities: instances.map((r: any) => r._instance_identity),
});

// 技能召回
const skills = await bkn.context.findSkills(knId, 'ot_customer', 5);

// 目录之外的工具：按名调用
const sql = await bkn.context.toolCall(knId, 'run_sql', { sql: 'SELECT 1' });
```

---

### curl

REST 面的路径是 `/api/agent-retrieval/v1/kn/<工具名>`，与 MCP 工具同名；健康检查在 `/health` 下，不带 `/api` 前缀。

> **先读这条**：自 0.1.3 起，`/kn/*` 的所有 POST 一律要求请求体带 `bkn_context`，缺失即 400。
> 这是无条件、fail-closed 的校验（`rest_public_handler.go:67` 无条件挂载中间件，
> `isLifecycleBusinessRequest` 对任意路径含 `/kn/` 的 POST 生效），所以**下面的 curl 示例
> 在按当前代码构建的部署上都会被挡下**。v0.1.2 发布版不含该中间件，示例可直接运行。
>
> `bkn_context` 只接受五个字段：`conversation_id`、`interaction_id`、`parent_operation_id`、
> `causation_event_ids`、`business_refs`。传入其它字段返回 `invalid_business_context`——
> 尤其注意 **`operation_key` 不能自己传**，它由服务端从请求关联信息推导。
> 缺 `conversation_id` 返回 `conversation_required`，缺 `interaction_id` 返回 `interaction_required`。
>
> 会话 id 的来源是 `bkn_start_interaction`，而该工具只在 MCP 通道上公开，没有对应的 REST 路由。
> 也就是说 0.1.3+ 上纯 HTTP 调用方拿不到这两个 id：请改走 MCP（`openbkn context ...` 即走此路径，
> 由服务端按连接自动归并会话）。

```bash
# 0.1.3+ 上的形态：bkn_context 由 MCP 会话提供，纯 curl 无法自行获得
# {"kn_id":"kn_abc123","query":"客户","bkn_context":{"conversation_id":"…","interaction_id":"…"}}
```

```bash
# 健康检查（无需鉴权）
curl -sk "https://<访问地址>/health/ready"
curl -sk "https://<访问地址>/health/alive"

# Schema 搜索
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn/search_schema" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{"kn_id":"kn_abc123","query":"客户订单关系"}'

# 查询对象实例
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn/query_object_instance" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "ot_id": "ot_customer",
    "filters": [{"field":"status","op":"==","value":"active"}],
    "limit": 20
  }'

# 查询实例子图
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn/query_instance_subgraph" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "relation_type_paths": [{
      "object_types": [
        {"id":"ot_customer","condition":{"operation":"and","sub_conditions":[]},"limit":10},
        {"id":"ot_order","condition":{"operation":"and","sub_conditions":[]},"limit":10}
      ],
      "relation_types": [{
        "relation_type_id":"rt_purchase",
        "source_object_type_id":"ot_customer",
        "target_object_type_id":"ot_order"
      }]
    }]
  }'

# 逻辑属性（注意路径是 logic-property-resolver，不与工具同名）
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn/logic-property-resolver" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "ot_id": "ot_customer",
    "query": "这批客户近一年的终身价值",
    "_instance_identities": [{"...": "取自 query_object_instance 返回的 _instance_identity"}],
    "properties": ["lifetime_value"]
  }'

# 行动信息
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn/get_action_info" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "at_id": "at_send_coupon",
    "_instance_identities": [{"...": "取自 query_object_instance 返回的 _instance_identity"}]
  }'
```

---

## 已下线命令对照

| 已下线 | 现行做法 |
| --- | --- |
| `openbkn context-loader config set/use/list/show/remove` | 取消，`kn-id` 改为每条命令的位置参数 |
| `openbkn context-loader tools` | `openbkn context tools <kn-id>`（部署级目录用 `openbkn context info`） |
| `openbkn context-loader kn-search` | `openbkn context search-schema <kn-id> <query>` |
| `openbkn context-loader kn-schema-search` | 同上，用 `--scope` 限定范围 |
| `openbkn context-loader query-object-instance '<json>'` | `openbkn context query-object-instance <kn-id> --args '<json>'` |
| `openbkn context-loader query-instance-subgraph '<json>'` | `openbkn context query-instance-subgraph <kn-id> --args '<json>'` |
| `openbkn context-loader get-logic-properties '<json>'` | `openbkn context get-logic-properties <kn-id> --args '<json>'` |
| `openbkn context-loader get-action-info '<json>'` | `openbkn context get-action-info <kn-id> --args '<json>'` |
