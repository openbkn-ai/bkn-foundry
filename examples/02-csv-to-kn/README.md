# 02 · From CSV Files to Knowledge Network

> Scattered spreadsheets, connected. No SQL required.

## The Problem

An HR director has employee, department, and project data split across three spreadsheets.
Understanding "who reports to whom" or "which projects are at risk of being understaffed"
means manual VLOOKUP chaining across files — tedious and error-prone.

This example imports those files into a knowledge network. Relationships are discovered
automatically. You can explore the schema, query instances, and traverse the org chart
to understand your people and projects.

## What This Example Does

```
CSV Files (local)
     │
     ▼
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│    MySQL     │────▶│ Vega Catalog │────▶│  Knowledge   │
│ (mysql load) │     │  (discover)  │     │   Network    │
└──────────────┘     └──────┬───────┘     └──────┬───────┘
                            │                    │
                            ▼                    ▼
                    ┌──────────────┐     ┌──────────────┐
                    │ Search index │     │   Schema +   │
                    │ (text+vector)│     │   instances  │
                    └──────────────┘     └──────────────┘
```

1. **Load** the CSV files into MySQL with the standard `mysql` client
2. **Register** a Vega catalog over that database and **discover** its tables
3. **Build** a Knowledge Network with object types bound to the discovered resources
4. **Build** the search index (full text + vector) for each resource
5. **Explore** the object types
6. **Query** object instances

> Object types bind to Vega *resource* IDs. Full-text and vector search need an
> **index**: a Vega BuildTask copies the rows into OpenSearch and vectorises the
> fields you name (Step 4). Index configuration is owned by the Vega *resource* —
> `index_config` plus per-field `features` — and the build task snapshots it at
> creation; `openbkn vega dataset build` writes both halves in one command.
>
> **Indexing changes how Step 6 reads.** Vega serves a table resource from its
> local index as soon as one exists, and queries the source database only while it
> does not. With the default `DO_INDEX=1`, Step 6 returns the build snapshot, and a
> later `UPDATE` in MySQL is invisible until the resource is rebuilt. Run with
> `DO_INDEX=0` to keep reads live — at the cost of full-text and vector search.
>
> Other Step 4 knobs: `EMBEDDING_MODEL_NAME=` (empty) builds full-text only,
> `INDEX_TIMEOUT` (default 300s) caps the wait per resource.

### Sample Data

| File | Contents |
|------|----------|
| `departments.csv` | 5 departments with budget and headcount |
| `employees.csv` | 16 employees with role, level, salary, manager |
| `projects.csv` | 8 projects with status, budget, owner |

## Prerequisites

```bash
# 1. Install the openbkn CLI
npm install -g @openbkn/bkn-sdk

# 2. Authenticate to a BKN Foundry
openbkn auth login https://<platform-url>

# 3. Prepare a MySQL database reachable from the platform
#    (the script creates tables automatically — no manual SQL needed)
```

## Quick Start

```bash
cp env.sample .env
# Fill in DB_HOST, DB_NAME, DB_USER, DB_PASS — see comments in env.sample
vim .env
./run.sh
```

> **Security:** `.env` is gitignored. Never commit credentials to version control.

### Using Your Own CSV Files

Replace the files in `data/` with your own CSVs. Requirements:
- First row must be a header
- File name becomes the table (and object type) name
- All columns are imported; numeric columns are detected automatically

## Key Commands

| Command | What it does |
|---------|-------------|
| `openbkn vega catalog create --connector-type mysql ...` | Register the database as a Vega catalog |
| `openbkn vega catalog discover <catalog-id> --wait` | Discover its tables as resources |
| `openbkn vega resource list --catalog-id <catalog-id> --category table` | List the discovered resource IDs |
| `openbkn bkn object-type create <kn-id> --resource-id <resource-id> ...` | Bind an object type to a resource |
| `openbkn vega dataset build <resource-id> --mode batch --build-key-fields id --fulltext-fields name --embedding-fields name --embedding-model <name> --wait` | Configure and run the search index |
| `openbkn vega dataset build-list --resource-id <resource-id>` | Check index build status |
| `openbkn bkn object-type list <kn-id>` | List object types |
| `openbkn bkn export <kn-id>` | Export KN definition |

## Differences from Example 01

| | 01-db-to-qa | 02-csv-to-kn |
|---|---|---|
| Data source | Existing MySQL database | Local CSV files |
| Ingestion | `seed.sql` into MySQL, then a Vega catalog | CSVs loaded into MySQL, then a Vega catalog |
| Schema setup | Write SQL seed file | Just bring CSVs |
| Network feature | Semantic search + Q&A | Multi-table schema + instance queries |
| Data domain | Supply chain (BOM, orders) | HR (employees, projects) |

## Cleanup

The KN and Vega catalog are **kept** after the run; their IDs are printed on exit.
Run with `CLEANUP=1 ./run.sh` to delete them automatically, or clean up by hand:

```bash
openbkn bkn delete <kn-id> -y
openbkn call /api/vega-backend/v1/catalogs/<catalog-id> -X DELETE
```
