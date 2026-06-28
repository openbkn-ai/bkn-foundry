# 🕸️ BKN engine

## 📖 Overview

The **Business Knowledge Network (BKN)** is the semantic layer of BKN Foundry. It models your domain with **object types**, **relation types**, and **action types**, stores **instances** and **relations**, and powers agents and analytics.

**Related modules:** [VEGA Engine](vega.md) (data behind views), [Context Loader](context-loader.md) (context from ontology).

**Operations note:** Semantic search is handled by **bkn-backend** and **ontology-query** together. Register an embedding in the **model factory** and set the default small-model name on both sides to match the registered `model_name`. See [Model configuration — Enable BKN semantic search](model.md#enable-bkn-semantic-search).

---

## 📝 BKN language

**BKN (Business Knowledge Network)** is a Markdown-based declarative modeling language for defining objects, relations, and actions in a business knowledge network. BKN describes model structure and semantics only — it contains no execution logic.

> Full BKN language specification is provided with your product documentation.

### Core Concepts

| Concept | Description |
|---------|-------------|
| **knowledge_network** | Top-level container for a business knowledge network |
| **object_type** | Business object type (e.g. Pod, Customer, Order) with properties and data source |
| **relation_type** | Link between two object types (e.g. "Pod belongs to Node", "Customer places Order") |
| **action_type** | Operation on an object, bindable to a tool or MCP |
| **risk_type** | Structured risk modeling for actions and objects |
| **concept_group** | Logical grouping of related object types |

### File Format

BKN files use the `.bkn` extension and UTF-8 encoding. Each file has two parts:

1. **YAML Frontmatter** — metadata wrapped in `---`
2. **Markdown Body** — definitions expressed with standard Markdown tables and headings

```markdown
---
type: object_type
id: pod
name: Pod
tags: [container, Kubernetes]
---

## ObjectType: Pod

The smallest deployable unit in Kubernetes.

### Data Properties

| Name | Display Name | Type | Description | Mapped Field |
|------|--------------|------|-------------|--------------|
| id | ID | integer | Primary key | id |
| pod_name | Pod Name | string | Pod name | pod_name |
| pod_status | Status | string | Running/Pending/Failed | pod_status |
| pod_node_name | Node | string | Node the pod runs on | pod_node_name |

### Keys

Primary Keys: id
Display Key: pod_name

### Data Source

| Type | ID | Name |
|------|-----|------|
| data_view | pod_info_view | pod_info_view |
```

Heading levels are fixed: `#` for network title, `##` for type definitions (`ObjectType:` / `RelationType:` / `ActionType:`), `###` for sections within a type (Data Properties, Keys, Endpoint, etc.).

### Directory Layout

Each object, relation, action, and risk gets its own file, organized into typed subdirectories:

```
my-network/
├── network.bkn              # Root file (type: knowledge_network)
├── SKILL.md                 # Agent entry point (optional, agentskills.io standard)
├── object_types/
│   ├── customer.bkn
│   └── order.bkn
├── relation_types/
│   └── customer_places_order.bkn
├── action_types/
│   └── check_order_status.bkn
├── concept_groups/
│   └── ecommerce.bkn
└── data/                    # Optional CSV instance data
    └── customers.csv
```

### Update Model

BKN uses a **no-patch update model**:

- **Add / modify** — edit `.bkn` files and import; upserts by `(network, type, id)`
- **Delete** — use the SDK/CLI delete API explicitly; deletion is not expressed in BKN files

### SDK

Official SDKs for parsing, validating, and transforming BKN files:

| Language | Package | Install |
|----------|---------|---------|
| TypeScript | [npm](https://www.npmjs.com/package/@openbkn/bkn-sdk) | `npm install @openbkn/bkn-sdk` |
| Golang | See your release notes | Follow the BKN SDK guide bundled with your distribution |

---

## 🤖 Create BKN with an agent

AI coding agents (Cursor, Claude Code, Codex, etc.) can generate spec-compliant BKN directories when the **openbkn** skill (`skills/openbkn/SKILL.md`) is installed.

### 📥 Install the skill

> The **openbkn** skill is distributed by your organization. For Cursor, place the skill directory under `~/.cursor/skills/` or `.cursor/skills/` in your project. Other agent environments follow their own skill-loading mechanism.

### Describe Your Domain in Natural Language

Simply tell the agent what you need. For example:

> Build a supply-chain knowledge network with "Material", "Warehouse", and "Inventory" objects.
> Material and Warehouse are linked through Inventory.
> Add a "Stock Check" action bound to the Inventory object.

The agent will automatically:
1. Read the BKN specification to confirm syntax rules
2. Generate `network.bkn` and per-type `.bkn` files in subdirectories
3. Generate a `SKILL.md` index file (agent-readable network navigation)
4. Cross-check ID references, heading levels, and required fields

### Validate and Push

Once BKN files are generated, validate and push to the platform with the CLI:

```bash
# Validate: check format and referential integrity
openbkn bkn validate ./my-network/

# Push to platform (creates or updates the knowledge network)
openbkn bkn push ./my-network/

# Pull an existing network from the platform to local
openbkn bkn pull <kn_id> ./export-dir/
```

### End-to-End Example

```bash
# 1. Generate BKN directory in the agent (interactive)
#    → agent creates ./supply-chain/

# 2. Validate
openbkn bkn validate ./supply-chain/

# 3. Push to platform
openbkn bkn push ./supply-chain/

# 4. List the created network
openbkn bkn list

# 5. Build indexes
openbkn bkn build <kn_id> --wait

# 6. Verify with semantic search
openbkn bkn search <kn_id> "materials running low on stock"
```

---

## 🔌 Create from data source

Instead of writing BKN files, you can generate a knowledge network directly from an existing data source:

```bash
# From a database: auto-discover table schemas and build indexes
openbkn bkn create-from-ds <ds_id> \
  --name "sales-network" \
  --tables orders,customers,products \
  --build --timeout 300

# From CSV files
openbkn bkn create-from-csv <ds_id> \
  --files "./data/*.csv" \
  --name "analytics-network" \
  --build
```

---

## 💻 CLI

### Knowledge Network Management

```bash
openbkn bkn list --name "order" --tag production --sort update_time --direction desc --limit 50 -v
openbkn bkn get <kn_id> --stats
openbkn bkn get <kn_id> --export
openbkn bkn pull <kn_id> ./export-dir/
```

### Build and Push

```bash
openbkn bkn build <kn_id> --wait --timeout 300
openbkn bkn validate ./my-network/
openbkn bkn push ./my-network/ --branch main
```

### Object Type CRUD

```bash
openbkn bkn object-type list <kn_id>
openbkn bkn object-type get <kn_id> <ot_id>

openbkn bkn object-type create <kn_id> \
  --name "Order" \
  --dataview-id <dv_id> \
  --primary-key "order_id" \
  --display-key "order_number" \
  --tags "commerce,core"

openbkn bkn object-type update <kn_id> <ot_id> \
  --add-property '{"name":"region","type":"string","display_name":"Region"}' \
  --update-property '{"name":"status","display_name":"Order Status"}' \
  --remove-property "legacy_field" \
  --tags "commerce,core,v2"

openbkn bkn object-type delete <kn_id> <ot_id>
```

### Query Object Instances

```bash
# Equality filter
openbkn bkn object-type query <kn_id> <ot_id> \
  '{"conditions":[{"field":"status","op":"==","value":"active"}],"limit":20}'

# Like filter
openbkn bkn object-type query <kn_id> <ot_id> \
  '{"conditions":[{"field":"name","op":"like","value":"%widget%"}],"limit":20}'

# IN filter
openbkn bkn object-type query <kn_id> <ot_id> \
  '{"conditions":[{"field":"region","op":"in","value":["US","EU"]}],"limit":20}'

# Compound: AND / OR
openbkn bkn object-type query <kn_id> <ot_id> '{
  "conditions": [
    {"field":"status","op":"==","value":"active"},
    {"field":"amount","op":">","value":1000}
  ],
  "logic": "and",
  "limit": 50
}'

# Pagination with search_after
openbkn bkn object-type query <kn_id> <ot_id> '{
  "limit": 50,
  "search_after": ["2026-04-01T00:00:00Z","order-9999"]
}'
```

### Relation Types

```bash
openbkn bkn relation-type list <kn_id>
openbkn bkn relation-type get <kn_id> <rt_id>

openbkn bkn relation-type create <kn_id> \
  --name "placed_by" \
  --source-type "Order" \
  --target-type "Customer" \
  --display-name "Placed By"

openbkn bkn relation-type delete <kn_id> <rt_id>
```

### Semantic Search

```bash
openbkn bkn search <kn_id> "overdue invoices in Q1"
openbkn bkn search <kn_id> "high-value customers" --limit 20
```

### Action Types and Execution

```bash
openbkn bkn action-type list <kn_id>
openbkn bkn action-type query <kn_id> '{"name":"calculate_risk"}'
openbkn bkn action-type execute <kn_id> <action_id> '{"input":{"customer_id":"C-1001"}}'

openbkn bkn action-log list <kn_id> --limit 20
openbkn bkn action-log get <kn_id> <log_id>
openbkn bkn action-log cancel <kn_id> <log_id>
openbkn bkn action-execution get <kn_id> <execution_id>
```

### End-to-End Example

```bash
# 1. Connect a MySQL data source
openbkn ds connect mysql db.example.com 3306 mydb --account root --password secret
# → ds_id: ds-abc123

# 2. Create a knowledge network from the data source
openbkn bkn create-from-ds ds-abc123 --name "ecommerce" --tables orders,customers --build --wait

# 3. Inspect the generated object types
openbkn bkn object-type list <kn_id>

# 4. Query customer instances
openbkn bkn object-type query <kn_id> <customer_ot_id> \
  '{"conditions":[{"field":"country","op":"==","value":"US"}],"limit":5}'

# 5. Semantic search across all types
openbkn bkn search <kn_id> "top spending customers"
```

---

## 📘 TypeScript SDK

> More runnable examples ship with the `@openbkn/bkn-sdk` npm package.

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

const knList = await bkn.kn.list({ limit: 50 });
for (const kn of knList) {
  console.log(`${kn.name} (${kn.id})`);
}

const knId = knList[0].id;
const detail = await bkn.kn.get(knId, { stats: true });

const objectTypes = await bkn.kn.objectTypes(knId);
for (const ot of objectTypes) {
  console.log(`${ot.name} (${ot.id})`);
}

const relationTypes = await bkn.kn.relationTypes(knId);
for (const rt of relationTypes) {
  console.log(`${rt.source_object_type?.name} —[${rt.name}]→ ${rt.target_object_type?.name}`);
}

const actionTypes = await bkn.kn.actionTypes(knId);

// Query object instances
const otId = objectTypes[0].id;
const instances = await bkn.kn.objectTypeQuery(knId, otId, {
  conditions: [{ field: 'status', op: '==', value: 'active' }],
  limit: 20,
});
console.log(instances);

// Subgraph traversal
const rt = relationTypes[0];
const subgraph = await bkn.kn.subgraph(knId, {
  relation_type_paths: [{
    relation_types: [{
      relation_type_id: rt.id,
      source_object_type_id: rt.source_object_type?.id,
      target_object_type_id: rt.target_object_type?.id,
    }],
  }],
  limit: 5,
});

// Semantic search
const result = await bkn.kn.search(knId, 'overdue invoices');
console.log(result);

// Action types + logs
const atId = actionTypes[0].id;
const actionDetail = await bkn.kn.actionTypeQuery(knId, atId, {});
const logs = await bkn.kn.actionLogs(knId, { limit: 5 });

// Build a KN's index by submitting a Vega BuildTask and waiting for completion
const buildTask = await bkn.vega.build(
  { resource_id: '<resource_id>', mode: 'batch' },
  { wait: true },
);
console.log('build:', buildTask);
```

---

## 🌐 curl

```bash
# List knowledge networks
curl -sk "https://<access-address>/api/ontology-manager/v1/knowledge-networks?name=order&sort=update_time&direction=desc&limit=50" \
  -H "Authorization: Bearer $(openbkn token)"

# Get a single knowledge network
curl -sk "https://<access-address>/api/ontology-manager/v1/knowledge-networks/kn-001" \
  -H "Authorization: Bearer $(openbkn token)"

# Create a knowledge network
curl -sk -X POST "https://<access-address>/api/ontology-manager/v1/knowledge-networks" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "ecommerce",
    "description": "E-commerce domain network",
    "ds_id": "ds-abc123",
    "tables": ["orders", "customers"]
  }'

# List object types
curl -sk "https://<access-address>/api/ontology-manager/v1/knowledge-networks/kn-001/object-types" \
  -H "Authorization: Bearer $(openbkn token)"

# Create an object type
curl -sk -X POST "https://<access-address>/api/ontology-manager/v1/knowledge-networks/kn-001/object-types" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Order",
    "dataview_id": "dv-xyz",
    "primary_key": "order_id",
    "display_key": "order_number",
    "properties": [
      {"name": "order_id", "type": "string"},
      {"name": "amount", "type": "number"},
      {"name": "status", "type": "string"}
    ]
  }'

# Query instances with conditions
curl -sk -X POST "https://<access-address>/api/ontology-query/v1/knowledge-networks/kn-001/object-types/ot-orders/instances/query" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "conditions": [
      {"field": "status", "op": "==", "value": "active"},
      {"field": "amount", "op": ">", "value": 1000}
    ],
    "logic": "and",
    "limit": 20,
    "search_after": null
  }'

# Semantic search across a network
curl -sk -X POST "https://<access-address>/api/ontology-query/v1/knowledge-networks/kn-001/search" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"query": "overdue invoices in Q1", "limit": 10}'

# List relation types
curl -sk "https://<access-address>/api/ontology-manager/v1/knowledge-networks/kn-001/relation-types" \
  -H "Authorization: Bearer $(openbkn token)"

# Execute an action type
curl -sk -X POST "https://<access-address>/api/bkn-backend/v1/knowledge-networks/kn-001/actions/act-risk/execute" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{"input": {"customer_id": "C-1001"}}'

# Get action execution result
curl -sk "https://<access-address>/api/bkn-backend/v1/knowledge-networks/kn-001/action-executions/exec-001" \
  -H "Authorization: Bearer $(openbkn token)"

# Build / rebuild network indexes
curl -sk -X POST "https://<access-address>/api/bkn-backend/v1/knowledge-networks/kn-001/build" \
  -H "Authorization: Bearer $(openbkn token)"
```
