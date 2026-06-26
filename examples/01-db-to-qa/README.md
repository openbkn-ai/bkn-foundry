# 01 · From Database to Semantic Search

> Turn a raw MySQL database into a searchable knowledge network — no SQL required.

## The Problem

A supply chain analyst has years of purchasing and inventory records locked in MySQL.
Every business question — "Which suppliers are most reliable?" "What's at risk of stockout?" —
means filing a request with the DBA and waiting hours for a custom query.

This example connects that database to a knowledge network. Discover the tables,
query them in real time, and search across them semantically — all grounded in your actual data.

## What This Example Does

```
MySQL Database
     │
     ▼
┌─────────────┐     ┌──────────────┐     ┌─────────────────┐
│ Vega Catalog│────▶│  Knowledge   │────▶│  Real-time      │
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
3. **Explore** the object types
4. **Query** the data in real time through the knowledge network
5. **Search** the knowledge network semantically

> This example uses the **Vega catalog/connector** model (vega-backend). Object types
> bind to Vega *resource* IDs and are queried in real time — no `bkn build` step. The
> legacy `data-connection` datasource flow is not used.

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

# 2. Build a KN with object types bound to Vega resources (real-time, no build)
openbkn bkn create --name "my-kn"
openbkn bkn object-type create <kn-id> --name 物料 --resource-id <resource-id> \
  --primary-key material_code --display-key material_name

# 3. Explore + query + search
openbkn bkn object-type list <kn-id>
openbkn bkn object-type query <kn-id> <ot-id> '{"limit":5}'
openbkn bkn search <kn-id> "物料"
```

## Troubleshooting

**`ERROR 1044 Access denied`** — the DB user has no rights on `DB_NAME`. Ask your DBA to run
`GRANT ALL ON your_db.* TO 'your_user'@'%';`

## Cleanup

Resources (KN, datasource) are deleted automatically on exit. Manual cleanup:
```bash
openbkn bkn delete <kn-id> -y
openbkn ds delete <datasource-id> -y
```
