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

Get a token with `openbkn token`. Once saved, Cursor will auto-discover the MCP tools exposed by Context Loader, and the agent can call them directly in conversation.

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

Once configured, MCP clients can discover and call these tools:

| Tool | Layer | Description |
|------|-------|-------------|
| `kn_search` | L1 | Semantic search across schema and instances |
| `kn_schema_search` | L1 | Search schema metadata only (discover candidate concepts) |
| `query_object_instance` | L2 | Query object instances with conditions |
| `query_instance_subgraph` | L2 | Query the relation subgraph around an instance |
| `get_logic_properties` | L3 | Get computed / derived property values |
| `get_action_info` | L3 | Get action type definition and parameter schema |

Every tool call requires the common parameter `kn_id` (knowledge network ID). Use `openbkn bkn list` to find it.

### Verify with CLI

You can verify the MCP server is working without configuring a full MCP client:

```bash
# Set the knowledge network
openbkn context-loader config set --kn-id kn_abc123

# List MCP tools
openbkn context-loader tools
```

---

## 💻 CLI

### Configuration Management

```bash
# Set active BKN Foundry server configuration
openbkn config set <alias> --url https://<access-address> --token <token>

# Use a saved configuration
openbkn config use <alias>

# List all saved configurations
openbkn config list

# Show current active configuration
openbkn config show

# Remove a saved configuration
openbkn config remove <alias>
```

### MCP Integration

The Context Loader exposes MCP (Model Context Protocol) endpoints for tool-aware LLM applications.

```bash
# List available MCP tools
openbkn mcp tools
```

### Knowledge Network Search

```bash
# Semantic search across a knowledge network
openbkn kn-search <kn_id> "quarterly revenue trends"
openbkn kn-search <kn_id> "customer churn risk factors" --limit 15
```

### Instance Queries

```bash
# Query a specific object instance by ID
openbkn query-object-instance <kn_id> <ot_id> <instance_id>

# Query the subgraph around an instance (neighbors, relations)
openbkn query-instance-subgraph <kn_id> <ot_id> <instance_id>
openbkn query-instance-subgraph <kn_id> <ot_id> <instance_id> --depth 2

# Get computed / logic properties for an instance
openbkn get-logic-properties <kn_id> <ot_id> <instance_id>
```

### Action Information

```bash
# Get full action type definition and parameter schema
openbkn get-action-info <kn_id> <action_id>
```

### End-to-End Example

```bash
# 1. Configure and authenticate
openbkn config set prod --url https://openbkn.example.com --token eyJ...
openbkn config use prod

# 2. Search the knowledge network for context
openbkn kn-search kn-001 "high-priority orders this month"

# 3. Drill into a specific instance
openbkn query-object-instance kn-001 ot-orders ord-5521

# 4. Explore its neighborhood
openbkn query-instance-subgraph kn-001 ot-orders ord-5521 --depth 2

# 5. Check logic properties
openbkn get-logic-properties kn-001 ot-orders ord-5521

# 6. Get action info before executing
openbkn get-action-info kn-001 act-escalate

# 7. List available MCP tools for agent integration
openbkn mcp tools
```

---

## TypeScript SDK

> More runnable examples ship with the `@openbkn/bkn-sdk` npm package.

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

const knId = 'kn-001';

// Semantic search across a knowledge network
const results = await bkn.kn.search(knId, 'quarterly revenue trends');
console.log('Search hits:', results);

// Context Loader retrieval runs over the agent-retrieval MCP endpoint.
// Endpoints without a typed helper are reachable via the generic passthrough.
const instance = await bkn.call(
  `/api/agent-retrieval/v1/knowledge-networks/${knId}/object-types/ot-orders/instances/ord-5521`,
  { method: 'GET' },
);
console.log('Instance:', instance);

const subgraph = await bkn.call(
  `/api/agent-retrieval/v1/knowledge-networks/${knId}/object-types/ot-orders/instances/ord-5521/subgraph`,
  { method: 'POST', body: { depth: 2 } },
);
console.log('Subgraph:', subgraph);
```

---

## curl

```bash
# Health check
curl -sk "https://<access-address>/api/agent-retrieval/v1/health" \
  -H "Authorization: Bearer $(openbkn token)"

# Semantic search across a knowledge network
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/knowledge-networks/kn-001/search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"query": "quarterly revenue trends", "limit": 10}'

# Query a specific object instance
curl -sk "https://<access-address>/api/agent-retrieval/v1/knowledge-networks/kn-001/object-types/ot-orders/instances/ord-5521" \
  -H "Authorization: Bearer $(openbkn token)"

# Query the subgraph around an instance
curl -sk -X POST "https://<access-address>/api/agent-retrieval/v1/knowledge-networks/kn-001/object-types/ot-orders/instances/ord-5521/subgraph" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"depth": 2}'

# Get logic properties
curl -sk "https://<access-address>/api/agent-retrieval/v1/knowledge-networks/kn-001/object-types/ot-orders/instances/ord-5521/logic-properties" \
  -H "Authorization: Bearer $(openbkn token)"

# Get action type information
curl -sk "https://<access-address>/api/agent-retrieval/v1/knowledge-networks/kn-001/actions/act-escalate/info" \
  -H "Authorization: Bearer $(openbkn token)"

# List MCP tools
curl -sk "https://<access-address>/api/agent-retrieval/v1/mcp/tools" \
  -H "Authorization: Bearer $(openbkn token)"
```
