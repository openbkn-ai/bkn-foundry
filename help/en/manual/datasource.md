# 📂 Data Ingestion (Vega Catalog)

## 📖 Overview

**Data ingestion** registers an external database with the platform, discovers its tables, and maintains the connection's lifecycle. It is the step before building a Knowledge Network (BKN): register the database as a **Catalog**, let discovery turn each table into a **Resource**, then bind object types to those resources.

> **Interface change:** the standalone data source service (`/api/builder/v1` and the `openbkn ds` commands) has been retired; its job now belongs to **vega-backend**. See [Retired command mapping](#retired-command-mapping) at the end.

Ingress prefix (typical):

| Prefix | Role |
| --- | --- |
| `/api/vega-backend/v1` | Catalog registration, table discovery, resource and index management |

**Related modules:** [BKN Engine](bkn.md) (create knowledge networks from a catalog), [VEGA Engine](vega.md) (resource queries and index builds).

## 🗃️ Supported database types

mysql, postgresql, sqlserver, oracle, clickhouse, hive, opensearch, elasticsearch, and more. Run `openbkn vega connector-type list` to see which connector types are installed on your platform.

## CLI

### Register a catalog

```bash
# MySQL
openbkn vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}'
# → returns a catalog id, e.g. d9okoc9v287s739h2120

# PostgreSQL
openbkn vega catalog create --name "analytics" --connector-type postgresql \
  --connector-config '{"host":"pg.example.com","port":5432,"username":"reader","password":"pass456","databases":["analytics"]}'
```

The fields inside `connector_config` vary by connector type; the schema returned by `openbkn vega connector-type get <type>` is authoritative. **The host must be reachable from the network vega-backend runs in** — usually an internal address, not the one your laptop can reach.

### Enable and discover

A catalog is created disabled. Enable it before discovery:

```bash
openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait
```

Discovery is asynchronous; `--wait` blocks until it finishes.

### List and inspect

```bash
# All catalogs
openbkn vega catalog list

# One catalog
openbkn vega catalog get <catalog_id>

# Connection health
openbkn vega catalog health <catalog_id>

# Test the connection
openbkn vega catalog test-connection <catalog_id>
```

### Inspect tables (resources)

Every discovered table is a resource, and its id is what an object type binds to:

```bash
# Table resources under this catalog
openbkn vega resource list --catalog-id <catalog_id> --category table

# One resource, including schema_definition (the field list)
openbkn vega resource get <resource_id>

# Sample rows
openbkn vega resource query <resource_id> --limit 10
```

### Import CSV

The platform-side CSV import command is gone. Load the CSVs into the target database with the standard `mysql` client first, then register the catalog and discover.

```bash
# CSV → MySQL → Catalog → knowledge network
# 1. Load the CSVs with the mysql client (bring your own DDL)
mysql -h db.example.com -u root -p supply_chain < load_csv.sql

# 2. Register the catalog, enable it, and discover (re-discover after schema changes)
openbkn vega catalog create --name "supply" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["supply_chain"]}'
openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait

# 3. Build the knowledge network from the catalog
openbkn bkn create-from-catalog <catalog_id> --name "supply-chain" --build
```

### Delete a catalog

```bash
openbkn vega catalog delete <catalog_id>
```

Deleting a catalog cascades to its resources and their indexes. Object types bound to those resources lose their data source and need rebinding.

### End-to-end

```bash
# 1. Register and enable
CAT=$(openbkn --json vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}' \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
openbkn vega catalog enable "$CAT"

# 2. Discover tables
openbkn vega catalog discover "$CAT" --wait
openbkn vega resource list --catalog-id "$CAT" --category table

# 3. Build a knowledge network from the catalog (--build also creates search indexes)
openbkn bkn create-from-catalog "$CAT" \
  --name "erp-supply-chain" \
  --tables "orders,products,customers" \
  --build --embedding-model text-embedding-v4

# 4. Verify
openbkn bkn object-type list <kn_id>
openbkn bkn search <kn_id> "overdue orders"
```

---

### TypeScript SDK

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<platform-url>', token: process.env.BKN_TOKEN });

// List catalogs
const catalogs = await bkn.vega.catalogs({ limit: 20 });
console.log('catalogs:', catalogs);

// Register a catalog
const created = await bkn.vega.createCatalog({
  name: 'erp',
  connector_type: 'mysql',
  connector_config: {
    host: 'db.example.com',
    port: 3306,
    username: 'root',
    password: 'pass123',
    databases: ['erp'],
  },
});
console.log('catalog id:', created.id);

// Enable and discover
await bkn.vega.enableCatalog(created.id);
await bkn.vega.discoverCatalog(created.id, true);

// List table resources
const resources = await bkn.resource.list({ catalogId: created.id, category: 'table' });
console.log('resources:', resources);

// Delete
await bkn.vega.deleteCatalog(created.id);
```

---

### curl

```bash
# List catalogs
curl -sk "https://<platform-url>/api/vega-backend/v1/catalogs?limit=20" \
  -H "Authorization: Bearer $(openbkn auth token)"

# Register a catalog
curl -sk -X POST "https://<platform-url>/api/vega-backend/v1/catalogs" \
  -H "Authorization: Bearer $(openbkn auth token)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "erp",
    "connector_type": "mysql",
    "connector_config": {
      "host": "db.example.com",
      "port": 3306,
      "username": "root",
      "password": "pass123",
      "databases": ["erp"]
    }
  }'

# Enable and discover
curl -sk -X POST "https://<platform-url>/api/vega-backend/v1/catalogs/<catalog_id>/enable" \
  -H "Authorization: Bearer $(openbkn auth token)"
curl -sk -X POST "https://<platform-url>/api/vega-backend/v1/catalogs/<catalog_id>/discover?wait=true" \
  -H "Authorization: Bearer $(openbkn auth token)"

# List table resources
curl -sk "https://<platform-url>/api/vega-backend/v1/resources?catalog_id=<catalog_id>&category=table" \
  -H "Authorization: Bearer $(openbkn auth token)"

# Delete a catalog
curl -sk -X DELETE "https://<platform-url>/api/vega-backend/v1/catalogs/<catalog_id>" \
  -H "Authorization: Bearer $(openbkn auth token)"
```

---

## Retired command mapping

`/api/builder/v1` and the `openbkn ds` commands were retired along with the data source service. Old command → current practice:

| Retired | Current practice |
| --- | --- |
| `openbkn ds connect <type> <host> <port> <db>` | `openbkn vega catalog create --connector-type <type> --connector-config '<json>'`, then `enable` |
| `openbkn ds list` / `openbkn ds get <id>` | `openbkn vega catalog list` / `openbkn vega catalog get <id>` |
| `openbkn ds tables <id>` | `openbkn vega catalog discover <id> --wait`, then `openbkn vega resource list --catalog-id <id> --category table` |
| `openbkn ds import-csv <id> --files ...` | Load the CSVs with the `mysql` client, then discover |
| `openbkn ds delete <id>` | `openbkn vega catalog delete <id>` |
| `openbkn bkn create-from-ds <ds_id>` | `openbkn bkn create-from-catalog <catalog_id>` |
