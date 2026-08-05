# 📚 Context Loader

## 📖 Overview

The **Context Loader** (including **agent-retrieval** services) assembles **high-quality context** for agents: ontology-aware recall, ranking, and on-demand loading from BKN and data plane. It sits between raw data/VEGA and the agent runtime.

The Context Loader is also exposed as an **MCP server**, providing **MCP tools** that coding agents and LLM-based applications can use directly.

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-retrieval/v1` | Retrieval and context assembly APIs |

**Related modules:** [BKN Engine](bkn.md), [VEGA Engine](vega.md).

---

## 🔌 MCP integration

The Context Loader exposes a standard [MCP (Model Context Protocol)](https://modelcontextprotocol.io) server over Streamable HTTP transport. AI coding tools (Cursor, Claude Desktop, Cline, etc.) and custom agents can call all Context Loader capabilities directly via the MCP protocol.

### Endpoint URL

```
https://<access-address>/api/agent-retrieval/v1/mcp
```

### Configure in Cursor

Create `.cursor/mcp.json` in your project root (or globally at `~/.cursor/mcp.json`):

```json
{
  "mcpServers": {
    "openbkn-context-loader": {
      "url": "https://<access-address>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

Get a token with `openbkn auth token`. Once saved, Cursor will auto-discover the MCP tools exposed by Context Loader, and the agent can call them directly in conversation.

### Configure in Claude Desktop

Edit `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "openbkn-context-loader": {
      "url": "https://<access-address>/api/agent-retrieval/v1/mcp",
      "headers": {
        "Authorization": "Bearer <token>"
      }
    }
  }
}
```

### Available MCP Tools

Once configured, MCP clients can discover and call these tools (your deployment is authoritative — run `openbkn context info` to see the live catalog):

| Tool | Purpose |
|------|---------|
| `search_schema` | Search object / relation / action / metric schemas |
| `get_kn_detail` | Fetch a KN's schema — `summary` skeleton, then drill down |
| `get_object_types` / `get_relation_types` | Full definitions for given ids |
| `query_object_instance` | Query object instances with conditions |
| `query_instance_subgraph` | Query the relation subgraph around instances |
| `get_logic_properties_values` | Compute derived property values |
| `get_action_info` | Get action type definition and parameter schema |
| `execute_action` | Execute an action type |
| `get_action_execution` / `list_action_executions` | Action execution status and history |
| `find_skills` | Recall skills bound to an object type |
| `list_knowledge_networks` | List knowledge networks |
| `list_resources` / `describe_resource` | List and describe Vega resources |
| `run_sql` | Run SQL directly against a resource |
| `bkn_start_interaction` / `bkn_finish_interaction` | Session lifecycle (business traceability) |

Every tool call requires `kn_id` (knowledge network ID). Use `openbkn bkn list` to find it.

### Verify with CLI

You can verify the MCP server without configuring a full MCP client. There is no global "current KN" setting — `kn-id` is a positional argument on every command:

```bash
# Deployment-wide tool catalog (no kn-id needed)
openbkn context info

# Tools advertised for one knowledge network's session
openbkn context tools <kn-id>
```

---

## 💻 CLI

The command group is `openbkn context`. (The earlier `openbkn context-loader` group was renamed, and its `config set/use/list/show/remove` subcommands are gone — `kn-id` is now a positional argument on every command.) Commands that take tool arguments accept them as `--args '<json>'`.

### Catalog and introspection

```bash
# Deployment-wide tool catalog (no kn-id)
openbkn context info

# Per-session tools / resources / prompts
openbkn context tools <kn-id>
openbkn context resources <kn-id>
openbkn context templates <kn-id>
openbkn context prompts <kn-id>
openbkn context prompt <kn-id> <name> --args '{"k":"v"}'
openbkn context resource <kn-id> <uri>
```

### Schema exploration

```bash
# Semantic schema search, optionally scoped
openbkn context search-schema <kn-id> "customer order relationships"
openbkn context search-schema <kn-id> "which object types describe a customer" \
  --scope object,relation --max 10

# Progressive schema: skeleton first, then drill down by id
openbkn context kn-detail <kn-id> --detail-level summary
openbkn context object-types <kn-id> ot_customer ot_order
openbkn context relation-types <kn-id> rt_purchase
```

### Instance queries

```bash
# Object instances with conditions
openbkn context query-object-instance <kn-id> --args '{
  "object_type_id": "ot_orders",
  "conditions": [{"field": "priority", "operation": "==", "value": "high"}],
  "limit": 20
}'

# Relation subgraph around instances
openbkn context query-instance-subgraph <kn-id> --args '{
  "object_type_id": "ot_orders",
  "instance_identities": [{"id": "ord-5521"}],
  "relation_type_ids": ["rt_belongs_to"],
  "limit": 50
}'
```

### Logic properties, actions, skills

```bash
# Computed / derived property values
openbkn context get-logic-properties <kn-id> --args '{
  "object_type_id": "ot_orders",
  "instance_identities": [{"id": "ord-5521"}],
  "logic_property_names": ["days_overdue"]
}'

# Actions available on an instance
openbkn context get-action-info <kn-id> --args '{
  "object_type_id": "ot_orders",
  "instance_identities": [{"id": "ord-5521"}]
}'

# Skills bound to an object type
openbkn context find-skills <kn-id> ot_orders --top-k 5
```

### Calling any tool

The catalog grows between releases and the CLI does not wrap every tool. `tool-call` invokes any MCP tool by name; `call-method` invokes any MCP method:

```bash
openbkn context tool-call <kn-id> run_sql --args '{"sql":"SELECT 1"}'
openbkn context tool-call <kn-id> execute_action --arg action_type_id=at_notify --arg dry_run=true
openbkn context call-method <kn-id> tools/list
```

### End-to-End Example

```bash
KN=<kn-id>

# 1. See which tools this session exposes
openbkn context tools "$KN"

# 2. Find the relevant object type
openbkn context search-schema "$KN" "high-priority orders" --scope object

# 3. Query instances
openbkn context query-object-instance "$KN" --args '{
  "object_type_id": "ot_orders",
  "conditions": [{"field": "priority", "operation": "==", "value": "high"}],
  "limit": 10
}'

# 4. Explore the neighbourhood
openbkn context query-instance-subgraph "$KN" --args '{
  "object_type_id": "ot_orders",
  "instance_identities": [{"id": "ord-5521"}]
}'

# 5. Check what can be done to it
openbkn context get-action-info "$KN" --args '{
  "object_type_id": "ot_orders",
  "instance_identities": [{"id": "ord-5521"}]
}'
```

---

## TypeScript SDK

> More runnable examples ship with the `@openbkn/bkn-sdk` npm package.

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

const knId = 'kn-001';

// Catalog
console.log(await bkn.context.info());
console.log(await bkn.context.tools(knId));

// Schema exploration
const schema = await bkn.context.searchSchema(knId, 'high-priority orders', { scope: ['object'] });
const skeleton = await bkn.context.knDetail(knId, 'summary');
const ots = await bkn.context.objectTypes(knId, ['ot_orders']);

// Instance queries
const instances = await bkn.context.queryObjectInstance(knId, {
  object_type_id: 'ot_orders',
  conditions: [{ field: 'priority', operation: '==', value: 'high' }],
  limit: 20,
});

const subgraph = await bkn.context.queryInstanceSubgraph(knId, {
  object_type_id: 'ot_orders',
  instance_identities: [{ id: 'ord-5521' }],
  relation_type_ids: ['rt_belongs_to'],
});

// Logic properties and actions
const logic = await bkn.context.logicProperties(knId, {
  object_type_id: 'ot_orders',
  instance_identities: [{ id: 'ord-5521' }],
  logic_property_names: ['days_overdue'],
});
const actions = await bkn.context.actionInfo(knId, {
  object_type_id: 'ot_orders',
  instance_identities: [{ id: 'ord-5521' }],
});

// Skills
const skills = await bkn.context.findSkills(knId, 'ot_orders', 5);

// Anything not wrapped: call the tool by name
const sql = await bkn.context.toolCall(knId, 'run_sql', { sql: 'SELECT 1' });
```

---

## curl

REST paths are `/api/agent-retrieval/v1/kn/<tool-name>`, matching the MCP tool names. Health checks live under `/health`, outside the `/api` prefix.

```bash
# Health check (no auth)
curl -sk "https://<access-address>/health/ready"
curl -sk "https://<access-address>/health/alive"

# Schema search
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/kn/search_schema" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{"kn_id":"kn-001","query":"high-priority orders"}'

# Query object instances
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/kn/query_object_instance" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn-001",
    "object_type_id": "ot_orders",
    "conditions": [{"field":"priority","operation":"==","value":"high"}],
    "limit": 20
  }'

# Query an instance subgraph
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/kn/query_instance_subgraph" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn-001",
    "object_type_id": "ot_orders",
    "instance_identities": [{"id":"ord-5521"}],
    "relation_type_ids": ["rt_belongs_to"]
  }'

# Logic properties (path is logic-property-resolver, not the tool name)
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/kn/logic-property-resolver" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn-001",
    "object_type_id": "ot_orders",
    "instance_identities": [{"id":"ord-5521"}],
    "logic_property_names": ["days_overdue"]
  }'

# Action info
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/kn/get_action_info" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "kn_id": "kn-001",
    "object_type_id": "ot_orders",
    "instance_identities": [{"id":"ord-5521"}]
  }'
```

> **Note:** some builds gate `POST /kn/*` on session context and answer
> `400 conversation_required` when the body has no `bkn_context`
> (`conversation_id` / `interaction_id` / `operation_key`). The v0.1.2 release does not
> have this check. Over the MCP transport the server derives the context per connection,
> so callers never supply it themselves.

---

## Retired command mapping

| Retired | Current practice |
| --- | --- |
| `openbkn context-loader config set/use/list/show/remove` | Gone; `kn-id` is a positional argument on every command |
| `openbkn context-loader tools` / `openbkn mcp tools` | `openbkn context tools <kn-id>` (deployment catalog: `openbkn context info`) |
| `openbkn kn-search <kn_id> <query>` | `openbkn context search-schema <kn-id> <query>` |
| `openbkn query-object-instance <kn_id> <ot_id> <id>` | `openbkn context query-object-instance <kn-id> --args '<json>'` |
| `openbkn query-instance-subgraph <kn_id> <ot_id> <id>` | `openbkn context query-instance-subgraph <kn-id> --args '<json>'` |
| `openbkn get-logic-properties <kn_id> <ot_id> <id>` | `openbkn context get-logic-properties <kn-id> --args '<json>'` |
| `openbkn get-action-info <kn_id> <action_id>` | `openbkn context get-action-info <kn-id> --args '<json>'` |
| `openbkn config set/use` (CLI profiles) | `openbkn auth login` / `openbkn auth use` / `openbkn auth list` |
