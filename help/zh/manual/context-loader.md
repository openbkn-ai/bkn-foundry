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
    "kweaver-context-loader": {
      "url": "https://<访问地址>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

Token 可通过 `openbkn token` 命令获取。配置保存后，Cursor 会自动发现 Context Loader 暴露的 MCP 工具，Agent 在对话中即可直接调用。

### 在 Claude Desktop 中配置

编辑 `claude_desktop_config.json`：

```json
{
  "mcpServers": {
    "kweaver-context-loader": {
      "url": "https://<访问地址>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

### MCP 工具列表

配置完成后，MCP 客户端可获取以下工具：

| 工具 | 层级 | 说明 |
|------|------|------|
| `kn_search` | L1 | 语义搜索知识网络 Schema 与实例 |
| `kn_schema_search` | L1 | 仅搜索 Schema 元数据（发现候选概念） |
| `query_object_instance` | L2 | 条件查询对象实例 |
| `query_instance_subgraph` | L2 | 查询实例的关系子图 |
| `get_logic_properties` | L3 | 获取逻辑属性（计算字段、派生指标） |
| `get_action_info` | L3 | 获取行动类信息与参数 Schema |

每个工具调用需要的公共参数：`kn_id`（知识网络 ID）。可通过 `openbkn bkn list` 获取。

### 使用 CLI 探测

不配置 MCP 客户端也可以通过 CLI 快速验证 MCP 服务是否正常：

```bash
# 设置知识网络
openbkn context-loader config set --kn-id kn_abc123

# 列出 MCP 工具
openbkn context-loader tools
```

---

## 💻 CLI

#### 配置管理

Context Loader CLI 需先指定目标知识网络：

```bash
# 设置当前使用的知识网络
openbkn context-loader config set --kn-id kn_abc123

# 切换到已保存的配置
openbkn context-loader config use my-config

# 列出所有已保存配置
openbkn context-loader config list

# 显示当前配置详情
openbkn context-loader config show

# 删除配置
openbkn context-loader config remove my-config
```

#### MCP 内省

查看 Context Loader 暴露的 MCP 能力：

```bash
# 列出所有可用工具（MCP tools）
openbkn context-loader tools
```

#### Layer 1 — Schema 搜索

在知识网络的 Schema 层做语义搜索，定位相关对象类与关系类：

```bash
# 全文搜索知识网络 Schema 与实例
openbkn context-loader kn-search "客户订单关系" --only-schema

# 仅搜索 Schema 元数据（对象类、关系类定义）
openbkn context-loader kn-schema-search "哪些对象类描述了客户"
```

#### Layer 2 — 实例查询

根据 Layer 1 定位到的对象类，查询具体实例数据：

```bash
# 条件查询对象实例
openbkn context-loader query-object-instance '{
  "kn_id": "kn_abc123",
  "object_type_id": "ot_customer",
  "conditions": [
    {"field": "status", "op": "==", "value": "active"},
    {"field": "region", "op": "in", "value": ["华东","华北"]}
  ],
  "logic": "and",
  "limit": 20
}'

# 查询实例的关系子图
openbkn context-loader query-instance-subgraph '{
  "kn_id": "kn_abc123",
  "instance_id": "cust_001",
  "depth": 2,
  "relation_types": ["rt_purchase", "rt_belongs_to"],
  "limit": 50
}'
```

#### Layer 3 — 逻辑属性与动作

获取计算字段和可执行动作信息：

```bash
# 获取逻辑属性（计算字段、派生属性）
openbkn context-loader get-logic-properties '{
  "kn_id": "kn_abc123",
  "object_type_id": "ot_customer",
  "instance_id": "cust_001",
  "properties": ["lifetime_value", "risk_score"]
}'

# 获取动作信息（该实例可触发的业务动作）
openbkn context-loader get-action-info '{
  "kn_id": "kn_abc123",
  "object_type_id": "ot_customer",
  "instance_id": "cust_001"
}'
```

#### 端到端流程

```bash
# 1. 配置知识网络
openbkn context-loader config set --kn-id kn_abc123

# 2. Schema 探索 — 找到相关对象类
openbkn context-loader kn-schema-search "订单和客户的关系"

# 3. 实例查询 — 获取活跃客户
openbkn context-loader query-object-instance '{
  "kn_id": "kn_abc123",
  "object_type_id": "ot_customer",
  "conditions": [{"field": "status", "op": "==", "value": "active"}],
  "limit": 5
}'

# 4. 子图扩展 — 查看客户的购买关系
openbkn context-loader query-instance-subgraph '{
  "kn_id": "kn_abc123",
  "instance_id": "cust_001",
  "depth": 1
}'

# 5. 获取动作信息 — 查看可对该客户执行的操作
openbkn context-loader get-action-info '{
  "kn_id": "kn_abc123",
  "object_type_id": "ot_customer",
  "instance_id": "cust_001"
}'
```

---

### TypeScript SDK

> 💡 更多可运行示例见随 `@openbkn/bkn-sdk` 包发布的示例目录。

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<访问地址>', token: process.env.BKN_TOKEN });

const knId = 'kn-001';

// Layer 1：Schema 搜索 — 用自然语言发现对象类
const schema = await bkn.context.searchSchema(knId, '客户订单关系');
console.log('Schema 搜索结果:', schema);

// Layer 2：实例查询 — 根据 Layer 1 找到的对象类查询具体数据
const instances = await bkn.context.queryObjectInstance(knId, {
  ot_id: 'ot_customer',
  limit: 20,
});
console.log('实例:', instances);

// 跨知识网络的语义搜索
const results = await bkn.kn.search(knId, '高价值客户');
console.log('搜索命中:', results);

// 子图遍历 — 沿关系类展开（按实例）
const subgraph = await bkn.context.queryInstanceSubgraph(knId, {
  ot_id: 'ot_customer',
  instance_id: 'cust-5521',
  depth: 2,
});
console.log('子图:', subgraph);
```

---

### curl

```bash
# 健康检查
curl -sk "https://<访问地址>/api/agent-retrieval/v1/health" \
  -H "Authorization: Bearer $(openbkn token)"

# Schema 搜索
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/kn-search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "query": "客户订单关系",
    "only_schema": true,
    "limit": 10
  }'

# 查询对象实例
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/query-object-instance" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "object_type_id": "ot_customer",
    "conditions": [
      {"field": "status", "op": "==", "value": "active"}
    ],
    "limit": 20
  }'

# 查询实例子图
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/query-instance-subgraph" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "instance_id": "cust_001",
    "depth": 2,
    "relation_types": ["rt_purchase"],
    "limit": 50
  }'

# 获取逻辑属性
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/get-logic-properties" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "object_type_id": "ot_customer",
    "instance_id": "cust_001",
    "properties": ["lifetime_value", "risk_score"]
  }'

# 获取动作信息
curl -sk -X POST "https://<访问地址>/api/agent-retrieval/v1/get-action-info" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn_abc123",
    "object_type_id": "ot_customer",
    "instance_id": "cust_001"
  }'
```
