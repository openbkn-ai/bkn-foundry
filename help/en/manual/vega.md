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

### Health and diagnostics

```bash
# Reachability probe (Node CLI: GET .../catalogs?limit=1 with auth)
openbkn vega health

# Catalog count (lists up to 100 catalogs and counts entries)
openbkn vega stats

# Health probe JSON + catalog_count (same catalog list cap)
openbkn vega inspect
```

The CLI does not call `GET /health` on the vega-backend pod; it uses an authenticated **catalogs list** probe. The raw `GET /api/vega-backend/v1/health` route is reachable directly (e.g. via the SDK's `call` passthrough) when that route is exposed behind your ingress.

### Catalog management

```bash
# List catalogs (optional filters)
openbkn vega catalog list
openbkn vega catalog list --status healthy --limit 20

# Get one catalog
openbkn vega catalog get <catalog_id>

# Health status for one or more catalogs, or all
openbkn vega catalog health cat_pg001 cat_mysql002
openbkn vega catalog health --all

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

openbkn vega catalog delete <catalog_id> [<catalog_id> ...]   # prompts unless -y
openbkn vega catalog delete cat_a,cat_b -y
```

### Resource operations

There is **no** `openbkn vega resource preview` subcommand. Use **`resource query`** with a small `limit` to sample rows.

```bash
# List resources (optional filters)
openbkn vega resource list
openbkn vega resource list --catalog-id <catalog_id> --category table --limit 50

# List all resources (GET .../resources/list)
openbkn vega resource list-all [--limit N] [--offset N]

openbkn vega resource get <resource_id>

# Structured data query (POST .../resources/:id/data)
openbkn vega resource query <resource_id> \
  -d '{"limit":10,"offset":0,"need_total":true}'

# Create / update / delete resources
openbkn vega resource create \
  --catalog-id <catalog_id> \
  --name my_table \
  --category table \
  [--source-identifier <si>] [--database <db>] [-d '{"extra":"fields"}']

openbkn vega resource update <resource_id> [--name X] [--status X] [--tags t1,t2] [-d '{"k":"v"}']

openbkn vega resource delete <resource_id> [<resource_id> ...] [-y]
```

### Dataset (documents and build)

For dataset-type resources, manage indexed documents and async build jobs:

```bash
openbkn vega dataset create-docs <resource_id> -d '[{"id":"doc1",...},...]'
openbkn vega dataset update-docs <resource_id> -d '[{"id":"doc1",...},...]'
openbkn vega dataset delete-docs <resource_id> <doc_id> [<doc_id> ...]
openbkn vega dataset delete-docs-query <resource_id> -d '{"filter":...}'

openbkn vega dataset build <resource_id> [--mode full|incremental|realtime]
openbkn vega dataset build-status <resource_id> <task_id>
```

### Structured query and SQL (vega-backend)

Both commands below use **`vega-backend`** only and **do not** require `vega-calculate-coordinator` (Trino). Use them on Core-only installs with MySQL/PostgreSQL catalogs.

**Structured query** — `POST /api/vega-backend/v1/query/execute`

```bash
openbkn vega query execute -d '<json>'
```

Body highlights: `tables` (required: `resource_id` + optional `alias`), `joins` (multi-table within one catalog), `output_fields`, `filter_condition`, `sort`, `offset` / `limit` (max 10000), `need_total`. Omit `query_id` on the first page; reuse it when paging. In `joins[].on`, **`left_field` / `right_field` must match `schema_definition[].name` from `openbkn vega resource get`**. All tables must share one catalog (501 otherwise).

Common `filter_condition` operations: `==`/`eq`, `!=`/`not_eq`, `>`/`gt`, `>=`/`gte`, `<`/`lt`, `<=`/`lte`, `in`/`not_in`, `like`/`not_like` (only if the field is typed as string in schema), `range`, `null`/`not_null`, nested `and`/`or` via `sub_conditions`. Leaf nodes usually include `field`, `operation`, `value`, `value_from` (`"const"` for literals).

Single-table example:

```bash
openbkn vega query execute -d '{"tables":[{"resource_id":"res_mysql_supplier"}],"limit":5,"need_total":true}'
```

Two-table JOIN (replace IDs and field names):

```bash
openbkn vega query execute -d '{
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
openbkn vega sql --resource-type mysql --query "SELECT * FROM {{.res_mysql_supplier}} LIMIT 5"
```

**Advanced mode**: full JSON body; when `-d` is present, `--resource-type` / `--query` are ignored.

```bash
openbkn vega sql -d '<json>'
openbkn vega sql --help
```

Required in the JSON body: `query` (SQL string or OpenSearch DSL object), `resource_type` (`mysql` | `mariadb` | `postgresql` | `opensearch`). Optional: `stream_size` (100–10000), `query_timeout` (seconds 1–3600), `query_id`.

Placeholders: `{{.<resource_id>}}` or `{{<resource_id>}}` (Vega resource id) are replaced with the resource’s physical table id. You may also run **native SQL** without placeholders if table names are valid for the engine.

**Comparison**

| Approach | Entry | Depends on | Typical use |
|----------|-------|------------|---------------|
| Structured | `openbkn vega query execute` | vega-backend | Same-catalog JOINs, filter DSL |
| Direct SQL | `openbkn vega sql` | vega-backend | Complex SQL, aggregations, placeholders |
| Resource data | `openbkn vega resource query <id> -d {...}` | vega-backend | Single resource, filters, `search_after` |
| Dataview `--sql` | `openbkn dataview query ... --sql` | mdl-uniquery + **Trino** (Etrino) | Cross-engine SQL via coordinator |

TypeScript: `bkn.vega.sql(body)` for direct SQL; the structured `query/execute` endpoint has no typed helper — reach it via `bkn.call('/api/vega-backend/v1/query/execute', { method: 'POST', body })`.

### Connector types

```bash
openbkn vega connector-type list
openbkn vega connector-type get mysql

openbkn vega connector-type register -d '{"name":"custom",...}'
openbkn vega connector-type update <type> -d '{...}'
openbkn vega connector-type delete <type> [-y]
openbkn vega connector-type enable <type> --enabled true
```

### Dataview operations

Data views are served by **mdl-uniquery** (not the vega-backend router). Use the **`dataview`** command group:

```bash
openbkn dataview list
openbkn dataview find --name "order"
openbkn dataview get <dataview_id>
# In --sql, FROM must be fully qualified: use meta_table_name from get; mysql_demo."sales"."orders" is illustrative
openbkn dataview query <dataview_id> --sql "SELECT order_id, amount FROM mysql_demo.\"sales\".\"orders\" WHERE status = 'active' LIMIT 10"
openbkn dataview query <dataview_id> --sql "SELECT COUNT(*) AS total FROM mysql_demo.\"sales\".\"orders\""
```

**Custom SQL (`--sql`) and Etrino**: Without `--sql`, `dataview query` uses the view’s stored definition and talks to the data source directly. With `--sql`, traffic goes through **`vega-calculate-coordinator`** (Hetu/Presto–style engine), which is **not** in the default BKN Foundry manifest. Install the **Etrino** charts: `vega-hdfs`, `vega-calculate` (includes the coordinator), and `vega-metadata`. Run `./deploy.sh etrino install` from the `deploy` directory to install Etrino only. **Use fully-qualified `catalog."schema"."table"` names for ad-hoc SQL.** See **Optional: Etrino** in [Install and deploy](../install.md).

**`dataview get` response fields (for custom `--sql`)**: The JSON from `openbkn dataview get <view_id> --pretty` includes the following; names match REST and the TypeScript SDK.

| Field | Role |
|-------|------|
| **`meta_table_name`** | **Required for ad-hoc SQL**: the fully-qualified table name string (`catalog."schema"."table"`). Use it verbatim in `FROM` / `JOIN`; do not guess the catalog from the view’s logical name. |
| **`sql_str`** | The stored view SQL; cross-check table references with `meta_table_name`. |
| **`fields`** | Column metadata (`name`, `type`, etc.); **does not** include the fully-qualified table name. |

**Where `meta_table_name` / fully-qualified names come from**: With `--sql`, the engine resolves identifiers in **Trino/Hetu style** as **`catalog."schema"."table"`**:

- **catalog**: The **Vega catalog id** for that data source (same as the **id** from `openbkn vega catalog list`; often prefixed by connector family, e.g. `mysql_…`, depending on the environment).
- **schema**: The source **namespace**—for MySQL this is usually the **database** name; for PostgreSQL, often the **schema** name; exact semantics follow the connector metadata.
- **table**: Physical table (or view) name.

When a data view is created, the platform materializes this into **`meta_table_name`** (and the built-in **`sql_str`**). **Do not guess the catalog segment from the view’s logical name alone**; run **`openbkn dataview get <view_id>`** and reuse **`meta_table_name`** verbatim in `FROM` / `JOIN`, or match the table references in **`sql_str`**. Multi-table joins must stay within one data source (one catalog).

Data view (mdl-uniquery) endpoints have no typed SDK helper — use the `openbkn dataview` CLI, or reach the REST paths via the SDK's `bkn.call(...)` passthrough.

### End-to-end example

```bash
openbkn vega health
openbkn vega connector-type list
openbkn vega catalog health --all
openbkn vega catalog discover <catalog_id> --wait
openbkn vega catalog resources <catalog_id> --category table
openbkn vega resource query <resource_id> -d '{"limit":5,"need_total":true}'
openbkn dataview find --name "orders"
openbkn dataview query <dataview_id> --sql "SELECT customer_id, SUM(amount) AS total FROM mysql_demo.\"sales\".\"orders\" GROUP BY customer_id LIMIT 10"
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

After `openbkn auth login`, use **`Authorization: Bearer $(openbkn token)`** for protected calls. Replace **`https://<access-address>`** with your deployment URL.

```bash
# Probe catalogs list (same idea as `openbkn vega health` in Node CLI)
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs?limit=1" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "x-business-domain: bd_public"

# Optional: raw pod health (path is /health on vega-backend, not under /v1)
# curl -sk "https://<access-address>/health" -H "Authorization: Bearer $(openbkn token)"

# List / get catalogs
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs?status=healthy&limit=20" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

# Create / update / delete catalog
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"my","connector_type":"mysql","connector_config":{"host":"h","port":3306,"database":"d","username":"u","password":"p"}}'
curl -sk -X PUT "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"name":"new-name"}'
curl -sk -X DELETE "https://<access-address>/api/vega-backend/v1/catalogs/cat-001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

# Catalog health / test-connection / discover / resources
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/health-status" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/test-connection" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/discover" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/catalogs/cat-001/resources?category=table&limit=30" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

# Resources: list, list-all, get, create, update, delete, data
curl -sk "https://<access-address>/api/vega-backend/v1/resources?catalog_id=cat-001&limit=50" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"catalog_id":"cat-001","name":"t","category":"table"}'
curl -sk -X PUT "https://<access-address>/api/vega-backend/v1/resources/res-001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"status":"active"}'
curl -sk -X DELETE "https://<access-address>/api/vega-backend/v1/resources/res-001" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/res-001/data" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: GET" \
  -d '{"limit":10,"offset":0,"need_total":true}'

# Dataset docs (use POST override)
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/res-ds/data" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -H "x-http-method-override: POST" \
  -d '[{"id":"doc1","content":"..."}]'

# Dataset build task
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/build-tasks" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_id":"res-ds","mode":"full"}'
curl -sk "https://<access-address>/api/vega-backend/v1/build-tasks/<task-id>" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"

# Structured query / direct SQL
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/query/execute" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"tables":[{"resource_id":"res-001"}],"limit":5,"need_total":true}'
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/resources/query" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"resource_type":"mysql","query":"SELECT 1 AS one"}'

# Connector types
curl -sk "https://<access-address>/api/vega-backend/v1/connector-types" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
curl -sk "https://<access-address>/api/vega-backend/v1/connector-types/mysql" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public"
# curl -sk -X POST "https://<access-address>/api/vega-backend/v1/connector-types" \
#   -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
#   -H "Content-Type: application/json" \
#   -d '<connector-type-json>'
curl -sk -X POST "https://<access-address>/api/vega-backend/v1/connector-types/mysql/enabled" \
  -H "Authorization: Bearer $(openbkn token)" -H "x-business-domain: bd_public" \
  -H "Content-Type: application/json" \
  -d '{"enabled":true}'
```

Dataview HTTP paths are defined by **mdl-uniquery**, not vega-backend; use `openbkn dataview` or reach the REST paths via the SDK's `bkn.call(...)` passthrough.

Full details: npm package `@openbkn/bkn-sdk` and `openbkn vega --help` / `openbkn vega <subcommand> --help`.
