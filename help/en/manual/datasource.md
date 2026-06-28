# 📂 Data source management

## 📖 Overview

**Data Source Management (DS)** handles **connection registration**, **table discovery**, **CSV import**, and **lifecycle maintenance** for external databases. It is the prerequisite for building Knowledge Networks (BKN) — first connect a database to the platform, then use `bkn create-from-ds` or `bkn create-from-csv` to turn tables into a knowledge network.

Ingress prefix (typical):

| Prefix | Role |
| --- | --- |
| `/api/builder/v1` | Data source connections, discovery, and management |

**Related modules:** [BKN Engine](bkn.md) (create knowledge networks from data sources), [VEGA Engine](vega.md) (data virtualization and query).

## 🗃️ Supported database types

mysql, postgresql, sqlserver, oracle, clickhouse, hive, opensearch, elasticsearch, and more. Run `openbkn vega connector-type list` to see which connector types are installed on your platform.

## CLI

### Connect a Data Source

```bash
# Connect to MySQL
openbkn ds connect mysql db.example.com 3306 erp \
  --account root --password pass123
# → returns ds_id, e.g. ds-abc123

# Connect to PostgreSQL with schema and custom name
openbkn ds connect postgresql pg.example.com 5432 analytics \
  --account reader --password pass456 \
  --schema public --name "analytics-db"
```

Argument order: `<db_type> <host> <port> <database>`. Use `--account` and `--password` for credentials.

### List and Inspect Data Sources

```bash
# List all data sources
openbkn ds list

# Search by keyword
openbkn ds list --keyword "erp"

# Filter by type
openbkn ds list --type mysql

# Get details for a single data source
openbkn ds get ds-abc123
```

### Discover Tables

```bash
# List all tables in a data source
openbkn ds tables ds-abc123

# Search tables by keyword
openbkn ds tables ds-abc123 --keyword "order"
```

### Import CSV

Upload local CSV files into an existing data source (database), then use them to build a knowledge network.

```bash
# Import multiple CSV files
openbkn ds import-csv ds-abc123 --files "materials.csv,inventory.csv"

# Use glob patterns with a table prefix
openbkn ds import-csv ds-abc123 --files "*.csv" --table-prefix sc_

# Recreate existing tables (when column structure changed)
openbkn ds import-csv ds-abc123 --files "materials.csv" --recreate

# Adjust batch size for large files
openbkn ds import-csv ds-abc123 --files "big-table.csv" --batch-size 1000
```

| Parameter | Required | Default | Description |
| --- | --- | --- | --- |
| `datasource_id` | yes | — | Target data source ID |
| `--files` | yes | — | CSV file paths, comma-separated or glob |
| `--table-prefix` | no | `""` | Prefix for generated table names |
| `--batch-size` | no | 500 | Rows per write batch (1–10000) |
| `--recreate` | no | off | Send overwrite on first batch to recreate table |

### Delete a Data Source

```bash
# Delete (with confirmation prompt)
openbkn ds delete ds-abc123

# Skip confirmation
openbkn ds delete ds-abc123 --yes
```

### End-to-End Example

```bash
# 1. Connect to a database
openbkn ds connect mysql db.example.com 3306 erp \
  --account root --password pass123
# → ds-abc123

# 2. Discover tables
openbkn ds tables ds-abc123

# 3. Create a knowledge network from the data source
openbkn bkn create-from-ds ds-abc123 \
  --name "erp-supply-chain" \
  --tables "orders,products,customers" \
  --build --timeout 600

# 4. Verify the knowledge network
openbkn bkn object-type list <kn_id>
openbkn bkn search <kn_id> "overdue orders"
```

---

## TypeScript SDK

Data source operations live under the `builder/v1` API. Reach them through the
SDK's generic `call` passthrough.

```typescript
import { createClient } from '@openbkn/bkn-sdk';

const bkn = createClient({ baseUrl: 'https://<access-address>', token: process.env.BKN_TOKEN });

// List data sources
const dsList = await bkn.call('/api/builder/v1/datasources?page=1&size=20', { method: 'GET' });
console.log('data sources:', dsList);

// Get details
const detail = await bkn.call('/api/builder/v1/datasources/ds-abc123', { method: 'GET' });
console.log('details:', detail);

// Connect a new data source
const newDs = await bkn.call('/api/builder/v1/datasources', {
  method: 'POST',
  body: {
    type: 'mysql',
    host: 'db.example.com',
    port: 3306,
    database: 'erp',
    account: 'root',
    password: 'pass123',
  },
});
console.log('data source ID:', newDs);

// Discover tables
const tables = await bkn.call('/api/builder/v1/datasources/ds-abc123/tables', { method: 'GET' });
console.log('tables:', tables);

// Delete
await bkn.call('/api/builder/v1/datasources/ds-abc123', { method: 'DELETE' });
```

---

## curl

```bash
# List data sources
curl -sk "https://<access-address>/api/builder/v1/datasources?page=1&size=20" \
  -H "Authorization: Bearer $(openbkn token)"

# Filter by type
curl -sk "https://<access-address>/api/builder/v1/datasources?type=mysql&page=1&size=20" \
  -H "Authorization: Bearer $(openbkn token)"

# Get data source details
curl -sk "https://<access-address>/api/builder/v1/datasources/ds-abc123" \
  -H "Authorization: Bearer $(openbkn token)"

# Connect a new data source
curl -sk -X POST "https://<access-address>/api/builder/v1/datasources" \
  -H "Authorization: Bearer $(openbkn token)" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "mysql",
    "host": "db.example.com",
    "port": 3306,
    "database": "erp",
    "account": "root",
    "password": "pass123"
  }'

# Discover tables
curl -sk "https://<access-address>/api/builder/v1/datasources/ds-abc123/tables" \
  -H "Authorization: Bearer $(openbkn token)"

# Delete a data source
curl -sk -X DELETE "https://<access-address>/api/builder/v1/datasources/ds-abc123" \
  -H "Authorization: Bearer $(openbkn token)"
```
