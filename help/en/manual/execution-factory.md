# 🛠️ Execution Factory

## 📖 Overview

The **Execution Factory** registers and runs **toolbox tools**, **MCP servers**, and **skills** that agents invoke under policy. It bridges LLM planning to concrete business actions (HTTP tools, code runners, MCP integrations, etc.).

Typical ingress prefix:

| Prefix | Role |
| --- | --- |
| `/api/agent-operator-integration/v1` | Toolbox, MCP, and Skill management and tool execution surface |

## Cypher Functions

A function in a function toolbox has one of two script types: `python` (code run in the sandbox) or `cypher`. A Cypher function is one read-only Cypher statement, and its `$name` placeholders are its inputs. It does not run in the sandbox: the platform runs the statement in the knowledge network as the caller, so the caller's row and column permissions apply. It can back an object type's logic property, and agents and MCP call it like any other tool.

| Field | Meaning |
| --- | --- |
| `script_type` | `cypher` |
| `kn_id` | The knowledge network the statement reads |
| `code` | One read-only Cypher statement; see the [Cypher query manual](cypher.md) |
| `inputs` | Exactly the statement's `$name`s; each is a `string`, `number` or `boolean`, or an `array` of one of those for `IN $name`; all are required and all need a description |
| `result_shape` | `value`: returns `{"value": x}`, one scalar column, `null` for no row, an error for more than one row; `table`: returns `{columns, entries, limited}` |

Before saving, the platform has the knowledge network compile the statement without reading data. A syntax error, a write clause, an object type or property that does not exist, no read permission, or a `value` statement that returns several columns or a node, relationship or list is refused with the reason.

```bash
# Create a Cypher function in function toolbox <box_id>: how many materials a supplier supplies
curl -sk -X POST "https://<host>/api/agent-operator-integration/v1/tool-box/<box_id>/tool" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "metadata_type": "function",
    "function_input": {
      "name": "supplier_material_count",
      "description": "Number of materials a supplier supplies",
      "script_type": "cypher",
      "kn_id": "<kn_id>",
      "code": "MATCH (s:supplier {supplier_id: $id})-[:supplies]->(m:material) RETURN count(m)",
      "inputs": [{"name": "id", "type": "string", "required": true, "description": "supplier_id of the supplier"}],
      "result_shape": "value"
    }
  }'

# Call it once enabled, in a published toolbox: returns {"status_code": 200, "body": {"value": 12}, ...}
curl -sk -X POST "https://<host>/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"body": {"id": "S001"}}'
```

As a logic property:

- Start from the instance by primary key. Use `$id` for a single primary key; for a composite key use one parameter per key property, named after it, such as `$order_id` and `$line_no`. Bind each to its key property.
- Choose the `value` shape and set `ResultPath` to `$.value`.
- A logic property's value follows the permissions of whoever reads it: the knowledge network's proxy only checks that the logic property may call the function, and the statement runs as the reader, with row and property permissions applied, so data the reader cannot see is never counted. Each instance runs one query; prefer an object metric for a plain numeric aggregate.

Notes:

- An edit that leaves out `script_type` keeps the function's type. The script type cannot change between `python` and `cypher`; create a new function instead.
- Parameters replace values only, never object type, relation type or property names. A parameter cannot be `null`, so a filter that applies only when given needs two functions.
- Importing a toolbox does not check its statements, because the target environment may not have the network yet. A statement that no longer fits fails when it is called.
- `LIMIT` and `SKIP` take an integer or a `$parameter` (of type `number`, called with a non-negative integer), for a "first N" or paged function. A `table` statement without `LIMIT` returns at most 1000 rows, with `limited` set to `true` when there were more; a statement that writes its own `LIMIT` is cut there and `limited` does not say so. `LIMIT` is at most 10000.
- A `value` statement that takes one row with `LIMIT 1` needs an `ORDER BY`, or which row it gets is arbitrary; aggregate (`count()`, `max()` and the like) where you can.

## CLI

### Toolboxes and Tools

A toolbox groups tools into one publishable box. An `openapi` box proxies its tools to a service; a `function` box holds platform functions, written in Python or as a Cypher statement. A tool can be executed only when it is enabled and its toolbox is published.

```bash
# List toolboxes (the id is `box_id`)
openbkn toolbox list

# List the tools in a toolbox
openbkn tool list --toolbox <box_id>

# Inspect one tool: metadata, parameters, usage rule
openbkn tool get <tool_id> --toolbox <box_id>

# Execute an enabled tool in a published toolbox
openbkn tool execute <tool_id> --toolbox <box_id> \
  --body '{"city": "Shanghai", "units": "metric"}'
```

The `openbkn call` command invokes any Execution Factory API by path, acting as a lightweight HTTP client wired into the platform auth and routing:

```bash
openbkn call GET /api/agent-operator-integration/v1/tool-box/list
```

### Skill Management

Skills are packaged bundles of tools, prompts, and configuration that can be registered, discovered from the marketplace, and installed into your workspace.

```bash
# List installed skills
openbkn skill list

# Search the skill marketplace
openbkn skill market
openbkn skill market --query "data analysis"

# Register a new skill from a local SKILL.md
openbkn skill register ./my-skill/SKILL.md

# View skill content (metadata and tool definitions)
openbkn skill content <skill_id>

# Read a file within a skill package
openbkn skill read-file <skill_id> SKILL.md
openbkn skill read-file <skill_id> tools/analyze.py

# Install a skill from the marketplace
openbkn skill install <skill_id>
openbkn skill install <skill_id> --version 1.2.0
```

### End-to-End Example

```bash
# 1. Browse the marketplace for a useful skill
openbkn skill market --query "knowledge graph"

# 2. Install a skill
openbkn skill install skill-kg-analyzer

# 3. Verify it appears in local skills
openbkn skill list

# 4. View what tools the skill provides
openbkn skill content skill-kg-analyzer

# 5. Read the skill's documentation
openbkn skill read-file skill-kg-analyzer SKILL.md

# 6. List the tools of a toolbox
openbkn tool list --toolbox <box_id>

# 7. Execute a tool directly
openbkn tool execute <tool_id> --toolbox <box_id> \
  --body '{"sql": "SELECT COUNT(*) FROM orders WHERE status = '\''pending'\''"}'
```

---

## TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// List toolboxes (generic passthrough)
const toolboxes = await bkn.call('/api/agent-operator-integration/v1/tool-box/list', { method: 'GET' });
console.log('toolboxes:', toolboxes);

// Execute a tool in a published toolbox
const result = await bkn.call(
  '/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>',
  { method: 'POST', body: { body: { city: 'Shanghai', units: 'metric' } } },
);
console.log('result:', result);

// List skills
const skills = await bkn.skills.list();
console.log('skills:', skills);

// Marketplace search
const marketResults = await bkn.skills.market({ query: 'data analysis' });
console.log('market:', marketResults);

// Register a skill from a local directory
const registered = await bkn.skills.register('./my-skill');
console.log('registered:', registered);

// Get skill content
const content = await bkn.skills.content('skill-kg-analyzer');
console.log('content:', content);

// Read a file in a skill
const fileContent = await bkn.skills.readFile('skill-kg-analyzer', 'SKILL.md');
console.log('file:', fileContent);

// Download + install from marketplace into a target directory
await bkn.skills.install('skill-kg-analyzer', './installed/skill-kg-analyzer');
```

---

## curl

```bash
# List toolboxes
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/list" \
  -H "Authorization: Bearer $(openbkn token)"

# Get toolbox details
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>" \
  -H "Authorization: Bearer $(openbkn token)"

# List the tools in a toolbox
curl -sk "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>/tools/list?page=1&page_size=10" \
  -H "Authorization: Bearer $(openbkn token)"

# Execute an enabled tool in a published toolbox
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>/proxy/<tool_id>" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"body": {"city": "Shanghai", "units": "metric"}}'

# Delete a toolbox
curl -sk -X DELETE "https://<access-address>/api/agent-operator-integration/v1/tool-box/<box_id>" \
  -H "Authorization: Bearer $(openbkn token)"

# List skills
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills" \
  -H "Authorization: Bearer $(openbkn token)"

# Search marketplace
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/market?query=data+analysis" \
  -H "Authorization: Bearer $(openbkn token)"

# Get skill content
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/content" \
  -H "Authorization: Bearer $(openbkn token)"

# Read a file within a skill
curl -sk "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/files/SKILL.md" \
  -H "Authorization: Bearer $(openbkn token)"

# Install a skill
curl -sk -X POST "https://<access-address>/api/agent-operator-integration/v1/skills/skill-kg-analyzer/install" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"version": "1.2.0"}'
```
