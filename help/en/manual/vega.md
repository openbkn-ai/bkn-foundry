# 🗄️ VEGA Engine

## 📖 Overview

**VEGA** provides **data virtualization** over heterogeneous sources: **data connections**, **models**, and **views** (including atomic and composite views). Agents and applications query through a unified SQL-oriented surface instead of wiring each source by hand.

Ingress prefix (typical):

| Prefix | Role |
| --- | --- |
| `/api/vega-backend/v1` | VEGA backend — connections, metadata, query execution |

**Related modules:** [BKN Engine](bkn.md) (semantic layer on top of data), [Context Loader](context-loader.md).

The **curl** section at the end of this page is **optional** — use it only if you need raw HTTP or shell scripts. If you rely on the **`openbkn` CLI** or the TypeScript SDK, you can skip it.

---

## 💻 CLI

Common flags for all `openbkn vega` subcommands: `-bd` / `--biz-domain <s>` (default from `openbkn config`), `--pretty` (pretty-print JSON, default on). Run `openbkn vega --help` for the full command tree.

### Reachability

The CLI has no dedicated probe subcommand. An authenticated catalog listing tells you both that the service is reachable and that your token works:

```bash
# Reachability probe: a listing means the service is up and the token is valid
openbkn vega catalog list --limit 1

# Per-catalog connection health
openbkn vega catalog health <catalog_id> [<catalog_id> ...]
```

The vega-backend pod's own `GET /health` is not under `/api/vega-backend/v1` and is usually not exposed through the ingress; reach it from inside the cluster when troubleshooting.

### Catalog management

```bash
# List catalogs (optional filters)
openbkn vega catalog list
openbkn vega catalog list --health-check-status healthy --limit 20

# Get one catalog
openbkn vega catalog get <catalog_id>

# Health status for one or more catalogs, or all
openbkn vega catalog health cat_pg001 cat_mysql002
openbkn vega catalog health cat_pg001 cat_mysql002

# Test connectivity for an existing catalog (registered in Vega)
openbkn vega catalog test-connection <catalog_id>

# Discover metadata; optional wait
openbkn vega catalog discover <catalog_id>
openbkn vega catalog discover <catalog_id> --wait

# Resources under a catalog
openbkn vega catalog resources <catalog_id>
openbkn vega catalog resources <catalog_id> --category table --limit 30

# Create / update / delete catalogs
openbkn vega catalog create \
  --name my-mysql \
  --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"database":"mydb","username":"u","password":"p"}'

openbkn vega catalog update <catalog_id> --name new-name --connector-config '{"host":"..."}'

openbkn vega catalog delete <catalog_id>
openbkn vega catalog delete cat_a,cat_b -y
```

### Resource operations

Resources come from catalog discovery; the CLI offers no manual create or update. To sample rows, use `query` with a small `limit`.

`openbkn vega resource` is read-only (`list` / `get` / `query`). The top-level `openbkn resource` (alias `res`) adds two more: fuzzy find and delete. Both reach the same resources.

```bash
# List resources (optional filters)
openbkn vega resource list
openbkn vega resource list --catalog-id <catalog_id> --category table --limit 50

# One resource, including schema_definition and index_config
openbkn vega resource get <resource_id>

# Sample rows
openbkn vega resource query <resource_id> --limit 10 --need-total

# Find by name (fuzzy; --exact for strict)
openbkn resource find --name "customer_orders" --catalog-id <catalog_id>

# Delete a resource
openbkn resource delete <resource_id>
```

### Dataset (documents and build)

For dataset-type resources, manage indexed documents and async build jobs:

**Build a local index** (full text and/or vector) for a table or dataset resource.
Index configuration is owned by the *resource* — `index_config` (build key, default
analyzer/model) plus per-field `features` — and the build task snapshots it at creation.
`dataset build` writes both halves: it PUTs the resource, then creates and starts the task.

```bash
openbkn vega dataset build <resource_id> --mode batch|streaming \
  [--execute-type full|incremental] \
  [--build-key-fields <col>[,<col>...]] \
  [--fulltext-fields <col>[,...]] [--fulltext-analyzer <name>] \
  [--embedding-fields <col>[,...]] [--embedding-model <model-name>] \
  [--wait] [--timeout <seconds>]

openbkn vega dataset build-status <resource_id> <task_id>
openbkn vega dataset build-list [--resource-id <id>] [--catalog-id <id>] [--active]
openbkn vega dataset build-start <task_id> [--reset]
openbkn vega dataset build-stop <task_id>
openbkn vega dataset build-delete <task_id> [<task_id> ...]
```

- `--embedding-model` takes the model **name**; a raw model id is rejected.
- A resource with no `index_config.build_key_fields` is rejected with HTTP 400, which is
  why `--build-key-fields` is required the first time you build a resource.
- Building changes how the resource reads: Vega serves a table resource from its local
  index as soon as one exists, and queries the source database only while it does not.
  Source updates become visible on the next build.

Document-level management (dataset resources) has no CLI subcommand; drive the API directly:

```bash
openbkn call /api/vega-backend/v1/resources/<resource_id>/data -X POST -d '[{...}]'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data -X PUT  -d '[{...}]'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data/<doc_id> -X PUT -d '{...}'
openbkn call /api/vega-backend/v1/resources/<resource_id>/data/<doc_ids> -X DELETE
```

### Structured query and SQL (vega-backend)

Both commands below use **`vega-backend`** only and **do not** require `vega-calculate-coordinator` (Trino). Use them on Core-only installs with MySQL/PostgreSQL catalogs.

**Structured query** — `POST /api/vega-backend/v1/resources/query`

There is no typed subcommand; call the endpoint directly:

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST -d '<json>'
```

Body highlights: `tables` (required: `resource_id` + optional `alias`), `joins` (multi-table within one catalog), `output_fields`, `filter_condition`, `sort`, `offset` / `limit` (max 10000), `need_total`. Omit `query_id` on the first page; reuse it when paging. In `joins[].on`, **`left_field` / `right_field` must match `schema_definition[].name` from `openbkn vega resource get`**. All tables must share one catalog (501 otherwise).

Common `filter_condition` operations: `==`/`eq`, `!=`/`not_eq`, `>`/`gt`, `>=`/`gte`, `<`/`lt`, `<=`/`lte`, `in`/`not_in`, `like`/`not_like` (only if the field is typed as string in schema), `range`, `null`/`not_null`, nested `and`/`or` via `sub_conditions`. Leaf nodes usually include `field`, `operation`, `value`, `value_from` (`"const"` for literals).

Single-table example:

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST \
  -d '{"tables":[{"resource_id":"res_mysql_supplier"}],"limit":5,"need_total":true}'
```

Two-table JOIN (replace IDs and field names):

```bash
openbkn call /api/vega-backend/v1/resources/query -X POST -d '{
  "tables": [
    {"resource_id":"res_a","alias":"a"},
    {"resource_id":"res_b","alias":"b"}
  ],
  "joins":[{"type":"inner","left_table_alias":"a","right_table_alias":"b","on":[{"left_field":"fk_id","right_field":"id"}]}],
  "output_fields":["a.name","b.amount"],
  "limit":10
}'
```

**Direct SQL** — `POST /api/vega-backend/v1/resources/query`

**Simple mode** (no JSON body): pass engine type and query as separate flags (quote the SQL).

```bash
openbkn vega sql --input-dialect mysql --query "SELECT * FROM {{res_mysql_supplier}} LIMIT 5"
```

**Advanced mode**: full JSON body; when `-d` is present, the individual flags above are ignored.

```bash
openbkn vega sql -d '<json>'
openbkn vega sql --help
```

Other flags: `--limit` / `--offset` / `--need-total`, cursor paging via `--paging-mode cursor` with `--cursor` / `--keep-alive-sec`, and `--query-timeout-sec`.

Placeholders: `{{.<resource_id>}}` or `{{<resource_id>}}` (Vega resource id) are replaced with the resource’s physical table id. You may also run **native SQL** without placeholders if table names are valid for the engine.

**Comparison**

| Approach | Entry | Typical use |
|----------|-------|-------------|
| Structured | `openbkn call /api/vega-backend/v1/resources/query -X POST` | Same-catalog JOINs, filter DSL |
| Direct SQL | `openbkn vega sql` | Complex SQL, aggregations, placeholders |
| Resource sample | `openbkn vega resource query <id> --limit N` | Quick look at one resource |

All three depend on vega-backend only.

TypeScript: `bkn.vega.sql(body)` for direct SQL; the structured endpoint has no typed helper — reach it via `bkn.call('/api/vega-backend/v1/resources/query', { method: 'POST', body })`.

### Connector types

```bash
openbkn vega connector-type list
openbkn vega connector-type get mysql


```

### Data views (retired)

**This capability is gone.** Data views were served by mdl-uniquery / mdl-data-model, neither of which is published any more, and the `openbkn dataview` command group in CLI 0.1.2 is an empty shell with no subcommands. The `vega-calculate-coordinator` (Trino/Hetu-style engine) that custom SQL depended on is no longer installed by the deployment scripts either.

Current equivalents:

| Former use | Current practice |
|------------|------------------|
| Browse / query view data | Query the resource directly: `openbkn vega resource query <resource_id> --limit N` |
| Custom SQL over a view | `openbkn vega sql --input-dialect <dialect> --query "..."`, referencing resources with `{{<resource_id>}}` |
| Cross-table JOIN | `openbkn call /api/vega-backend/v1/resources/query -X POST`, joining tables within one catalog |

An object type still bound to a retired `data_view` data source will fail to query; rebind it to a Vega resource.

### End-to-end example

```bash
# 1. Reachability and connectors
openbkn vega catalog list --limit 1
openbkn vega connector-type list

# 2. Catalog health and discovery
openbkn vega catalog health <catalog_id>
openbkn vega catalog discover <catalog_id> --wait
openbkn vega catalog resources <catalog_id> --category table

# 3. Look at the data
openbkn vega resource query <resource_id> --limit 5 --need-total
openbkn vega sql --input-dialect mysql \
  --query "SELECT customer_id, SUM(amount) AS total FROM {{<resource_id>}} GROUP BY customer_id LIMIT 10"

# 4. Build the search index (full text + vector)
openbkn vega dataset build <resource_id> --mode batch --execute-type full \
  --build-key-fields id --fulltext-fields customer_name \
  --embedding-fields customer_name --embedding-model text-embedding-v4 --wait
```

---

## 📘 TypeScript SDK

`bkn.vega` exposes typed helpers for catalogs, connector types, direct SQL, and
build tasks. Resource browsing/querying lives under `bkn.resource`. Endpoints
without a typed helper (structured `query/execute`, catalog update/delete,
resource CRUD, dataset docs, data views) are reached through the generic
`bkn.call(...)` passthrough.

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// Catalogs (typed)
const catalogs = await bkn.vega.catalogs({ status: 'healthy', limit: 20 });
catalogs.forEach((c) => console.log(c.id, c.name));

const detail = await bkn.vega.getCatalog('cat-001');
const healthStatus = await bkn.vega.catalogHealth(['cat-001', 'cat-002']);
await bkn.vega.discoverCatalog('cat-001', true); // wait for discovery
const catRes = await bkn.vega.catalogResources('cat-001', 'table');

await bkn.vega.createCatalog({
  name: 'my-mysql',
  connector_type: 'mysql',
  connector_config: { host: 'db.example.com', port: 3306, database: 'mydb', username: 'u', password: 'p' },
});

// Catalog update / delete have no typed helper — use the passthrough
await bkn.call('/api/vega-backend/v1/catalogs/cat-001', { method: 'PUT', body: { name: 'renamed' } });
await bkn.call('/api/vega-backend/v1/catalogs/cat-001', { method: 'DELETE' });

// Resources (typed browse + query)
const resources = await bkn.resource.list({ catalogId: 'cat-001', category: 'table', limit: 50 });
const res = await bkn.resource.get('res-001');
const rows = await bkn.resource.query('res-001', { limit: 5 });

// Connector types (typed)
const connectors = await bkn.vega.connectorTypes();

// Direct SQL (typed)
const sqlOut = await bkn.vega.sql({
  resource_type: 'mysql',
  query: 'SELECT 1 AS one',
});

// Structured query/execute — no typed helper, use the passthrough
const structured = await bkn.call('/api/vega-backend/v1/query/execute', {
  method: 'POST',
  body: { tables: [{ resource_id: 'res-001' }], limit: 5, need_total: true },
});

// Dataset build task (typed)
const build = await bkn.vega.build({ resource_id: 'res-ds', mode: 'batch' }, { wait: true });
const status = await bkn.vega.buildStatus(String(build.id));

// Data views (mdl-uniquery) — no typed helper, use the passthrough or the `openbkn dataview` CLI
const dvList = await bkn.call('/api/mdl-uniquery/v1/dataviews?limit=50', { method: 'GET' });
```

---

## 🌐 curl

After `openbkn auth login`, use **`Authorization: Bearer $(openbkn auth token)`** for protected calls. Replace **`https://<access-address>`** with your deployment URL.

```bash
# Probe: a catalogs listing means the service is up and the token is valid
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs?limit=1" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "x-business-domain: bd_public"

# Optional: raw pod health (path is /health on vega-backend, not under /v1)
# curl -sk "https://<access-address>/health" -H "Authorization: Bearer $(openbkn auth token)"

# List / get catalogs
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs?status=healthy&limit=20" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

# Create / update / delete catalog
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"my","connector_type":"mysql","connector_config":{"host":"h","port":3306,"database":"d","username":"u","password":"p"}}'
curl -sk -X PUT "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"new-name"}'
curl -sk -X DELETE "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

# Catalog health / test-connection / discover / resources
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/health-status" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/test-connection" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/discover" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/resources?category=table&limit=30" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

# Resources: list, list-all, get, create, update, delete, data
curl -sk "https://<access-address>/api/vega-backend/v1/resources?catalog_id=cat-001&limit=50" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"catalog_id":"cat-001","name":"t","category":"table"}'
curl -sk -X PUT "https://<access-address>/api/vega-backend/v1/resources/res-001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}'
curl -sk -X DELETE "https://<access-address>/api/vega-backend/v1/resources/res-001" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/res-001/data" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: GET" \
  -d '{"limit":10,"offset":0,"need_total":true}'

# Dataset docs (use POST override)
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/res-ds/data" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: POST" \
  -d '[{"id":"doc1","content":"..."}]'

# Dataset build task
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/build-tasks" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_id":"res-ds","mode":"full"}'
curl -sk "https://<access-address>/api/vega-backend/v1/build-tasks/<task-id>" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"

# Structured query / direct SQL
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/query/execute" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"tables":[{"resource_id":"res-001"}],"limit":5,"need_total":true}'
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/query" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_type":"mysql","query":"SELECT 1 AS one"}'

# Connector types
curl -sk "https://<access-address>/api/vega-backend/v1/connector-types" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/connector-types/mysql" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public"
# curl -sk -X POST "https://<access-address>/api/vega-backend/v1/connector-types" \
#   -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
#   -H "Content-Type: application/json" \
#   -d '<connector-type-json>'
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/connector-types/mysql/enabled" \
  -H "Authorization: Bearer $(openbkn auth token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

Dataview HTTP paths are defined by **mdl-uniquery**, not vega-backend; use `openbkn dataview` or reach the REST paths via the SDK's `bkn.call(...)` passthrough.

Full details: npm package `@openbkn/bkn-sdk` and `openbkn vega --help` / `openbkn vega <subcommand> --help`.
