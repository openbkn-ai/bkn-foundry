# 01 · From Database to Semantic Search

> Turn a raw MySQL database into a searchable knowledge network — no SQL required.

## The Problem

A supply chain analyst has years of purchasing and inventory records locked in MySQL.
Every business question — "Which suppliers are most reliable?" "What's at risk of stockout?" —
means filing a request with the DBA and waiting hours for a custom query.

This example connects that database to a knowledge network. Discover the tables,
query them, and search across them semantically — all grounded in your actual data.

## What This Example Does

```
MySQL Database
     │
     ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│ Vega Catalog│────▶│  Knowledge   │────▶│  Instance       │
│ + Discover  │     │  Network     │     │  Query (Vega)   │
└─────────────┘     └──────────────┘     └─────────────────┘
                           │
                           ▼
                    ┌──────────────┐     ┌─────────────────┐
                    │   Schema     │     │   Semantic      │
                    │   Explore    │     │   Search        │
                    └──────────────┘     └─────────────────┘
```

0. **Seed** sample data into MySQL (`seed.sql` — fictional smart-home supply chain)
1. **Register** a Vega catalog (MySQL connector) and **discover** its tables
2. **Create** a Knowledge Network with object types bound to Vega resources
3. **Build** the search index (full text + vector) for each resource
4. **Explore** the object types
5. **Query** the data through the knowledge network
6. **Search** the knowledge network semantically

> This example uses the **Vega catalog/connector** model (vega-backend). Object types
> bind to Vega *resource* IDs; the legacy `data-connection` datasource flow is not used.
>
> Full-text and vector search need an **index**: a Vega BuildTask copies the
> resource's rows into OpenSearch and vectorises the fields you name (Step 3). Index
> configuration is owned by the Vega *resource* — `index_config` (build key, default
> analyzer/model) plus per-field `features` — and the build task snapshots it at
> creation. `openbkn vega dataset build` writes both halves in one command. There is
> no KN-level `bkn build`; that API was retired.
>
> **Indexing changes how the object type reads.** Vega serves a table resource from
> its local index as soon as one exists, and queries the source database only while
> it does not. So with the default `DO_INDEX=1`, Step 5 returns the build snapshot,
> and a later `UPDATE` in MySQL is invisible until the resource is rebuilt
> (`openbkn vega dataset build <resource-id> --mode batch --execute-type full`).
> Run with `DO_INDEX=0` to keep reads live — at the cost of full-text and vector search.
>
> Other Step 3 knobs: `EMBEDDING_MODEL_NAME=` (empty) builds full-text only,
> `INDEX_TIMEOUT` (default 300s) caps the wait per resource.
>
> Note: the built index is not yet visible to the knowledge network's semantic layer.
> Object-type properties do not advertise `match` / `knn` operations, so `bkn search`
> stays at schema-level concept matching — see the PR notes on `f_index_available`.

## Prerequisites

```bash
# 1. Install the openbkn CLI
npm install -g @openbkn/bkn-sdk

# 2. Install the MySQL client (for Step 0: seed.sql runs on your machine)
#    macOS:  brew install mysql-client
#    Ubuntu: sudo apt install -y mysql-client

# 3. Authenticate to a BKN Foundry
openbkn auth login https://<platform-url>

# 4. Prepare a MySQL database reachable from the platform
#    The DB user must have CREATE TABLE / INSERT / SELECT rights.
```

## Quick Start

```bash
cp env.sample .env
# Fill in DB_HOST, DB_NAME, DB_USER, DB_PASS — see comments in env.sample
vim .env
./run.sh
```

> **Security:** `.env` is gitignored. Never commit credentials to version control.

## Configuration Notes

**`DB_HOST` vs `DB_HOST_SEED`**
Step 0 runs `mysql` on your local machine; Step 1 uses the platform's network to connect.
If your laptop uses a public IP but the platform needs a VPC internal IP, set `DB_HOST`
to the internal address and `DB_HOST_SEED` to the public one.

**`DEBUG=1`** in `.env` prints verbose output (API bodies, openbkn config). Passwords are never logged.

## Key Commands

```bash
# 1. Register a Vega catalog (MySQL connector) and discover tables
openbkn vega catalog create --name "my-cat" --connector-type mysql \
  --connector-config '{"host":"'$DB_HOST'","port":'$DB_PORT',"username":"'$DB_USER'","password":"'$DB_PASS'","databases":["'$DB_NAME'"]}'
openbkn call "/api/vega-backend/v1/catalogs/<catalog-id>/enable" -X POST   # catalogs start disabled
openbkn vega catalog discover <catalog-id> --wait
openbkn vega resource list --catalog-id <catalog-id> --category table       # → resource IDs

# 2. Build a KN with object types bound to Vega resources
openbkn bkn create --name "my-kn"
openbkn bkn object-type create <kn-id> --name 物料 --resource-id <resource-id> \
  --primary-key material_code --display-key material_name

# 3. Build the search index for a resource (writes index_config, then runs the task)
openbkn vega dataset build <resource-id> --mode batch --execute-type full \
  --build-key-fields material_code \
  --fulltext-fields material_name,bom_material_code \
  --embedding-fields material_name --embedding-model text-embedding-v4 --wait
#    --embedding-model takes the model NAME (a raw model id is rejected)
#    check progress later: openbkn vega dataset build-list --resource-id <resource-id>

# 4. Explore + query + search
openbkn bkn object-type list <kn-id>
openbkn bkn object-type query <kn-id> <ot-id> '{"limit":5}'
openbkn bkn search <kn-id> "物料"
```

## Troubleshooting

**`ERROR 1044 Access denied`** — the DB user has no rights on `DB_NAME`. Ask your DBA to run
`GRANT ALL ON your_db.* TO 'your_user'@'%';`

## Cleanup

The knowledge network and the Vega catalog are **kept** after the run so you can inspect them;
their IDs are printed on exit. Run with `CLEANUP=1 ./run.sh` to delete them automatically instead.
Manual cleanup:

```bash
openbkn bkn delete <kn-id> -y
openbkn call /api/vega-backend/v1/catalogs/<catalog-id> -X DELETE
```
