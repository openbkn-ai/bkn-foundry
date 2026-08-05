# Build a knowledge network from CSV in one shot

> Start a new recipe by copying [`_TEMPLATE.md`](./_TEMPLATE.md). This page is the first concrete example showing how each section is filled in.

> - **Difficulty**: ⭐ Beginner
> - **Time**: ~ 10 minutes
> - **Modules touched**: `bkn`, `datasource`
> - **CLI version**: `openbkn >= 0.6`

## 1. Goal

**After this recipe you will have:** a knowledge network named `supply-kn` where each input CSV becomes one object type (OT), queryable via `bkn object-type query` and searchable via `bkn search` — all from a single command, with no hand-written schema.

## 2. Prerequisites

- Logged in via `openbkn auth login <platform-url>`.
- Correct business domain: `openbkn config show`; if it's wrong, run `openbkn config set-bd <uuid>`.
- A **datasource** that BKN Foundry can reach (the CSV files are imported into it first as the staging store).
- Your local CSV files (header on row 1, UTF-8). This recipe uses two files — `materials.csv` and `inventory.csv`, both with `material_code` and `material_name` columns.

## 3. Steps

### 3.1 Pick or create a catalog

List existing catalogs first:

```bash
openbkn vega catalog list
```

Register a new one if none fits (MySQL example):

```bash
openbkn vega catalog create --name "erp" --connector-type mysql \
  --connector-config '{"host":"db.example.com","port":3306,"username":"root","password":"pass123","databases":["erp"]}'
# → returns a catalog id

openbkn vega catalog enable <catalog_id>
openbkn vega catalog discover <catalog_id> --wait
```

> Record **`<catalog_id>`** — the rest of the recipe assumes it's already known.

### 3.2 One-shot: build a KN from CSV

```bash
openbkn bkn create-from-csv <catalog_id> \
  --files "materials.csv,inventory.csv" \
  --name "supply-kn" \
  --table-prefix sc_
# → Imports the CSVs, creates the dataview, the OTs, and runs the index build.
# → Returns kn_id.
```

Quick parameter reference:

| Parameter | Required | Description |
| --- | --- | --- |
| `<catalog_id>` | yes | Catalog whose database stages the CSVs |
| `--files` | yes | Comma-separated paths or a glob (e.g. `"*.csv"`) |
| `--name` | yes | Knowledge network name |
| `--table-prefix` | no | Prefix for staging tables (avoids name clashes) |
| `--build` / `--no-build` | no | `--build` by default; pass `--no-build` to skip |
| `--timeout` | no | Build wait timeout in seconds (default 300) |

<details>
<summary>Equivalent two-step path (use this when you want to override primary/display keys)</summary>

```

> **This command does not currently work**: its CSV loading depends on the retired dataflow path.
> Use the step-by-step route below (mysql client → re-discover → `create-from-catalog`);
> `examples/02-csv-to-kn` is a runnable version.bash
# 1. Load the CSVs with the mysql client (the platform-side import-csv is retired)
mysql -h db.example.com -u root -p erp < load_csv.sql

# 2. Re-discover tables, then build the KN from the catalog
openbkn vega catalog discover <catalog_id> --wait
openbkn bkn create-from-catalog <catalog_id> --name "supply-kn" --build
```

In the step-by-step path you can pass `--primary-key` / `--display-key` to `bkn object-type create` to pin the keys explicitly.

</details>

### 3.3 Verify

```bash
# List OTs — each CSV should yield one
openbkn bkn object-type list <kn_id>

# Sample query (always cap with limit to avoid wide-row JSON truncation)
openbkn bkn object-type query <kn_id> <ot_id> '{"limit":5}'

# Semantic search
openbkn bkn search <kn_id> "material"
```

## 4. Expected output

> **Success criterion**: `object-type query` returns `total > 0`, `datas[0]` contains the CSV columns you imported, and `bkn search` returns a non-empty `concepts` list.

`object-type query` should return something like:

```jsonc
{
  "total": 1234,
  "datas": [
    {
      "_instance_identity": "...",
      "material_code": "M-001",
      "material_name": "Screw",
      // ... other columns
    }
  ]
}
```

A non-empty `concepts` list from `bkn search` indicates the retrieval pipeline is healthy.

## 5. Troubleshooting

> The "Symptom" column lists **the literal output or error a reader will see**, so it can be search-matched directly.

| Symptom | Likely cause | Fix |
| --- | --- | --- |
| `401 Unauthorized`, or response body contains `oauth info is not active` | Token expired | `openbkn auth login <platform-url>` |
| `openbkn bkn object-type list <kn_id>` prints `[]` | Wrong path or glob matched nothing | Check `--files`; use absolute paths if needed |
| `object-type query` response shows `total = 0` | Empty source table, wrong mapping, or an index that never completed | Check the source with `openbkn vega resource query <resource_id> --limit 5`; if you built an index, check `index_health` via `openbkn vega dataset build-list --resource-id <resource_id>` |
| Loading CSVs reports `table already exists` | The table is already there | Add `DROP TABLE IF EXISTS` to the SQL (the platform-side `ds import-csv` is retired; loading is done by the mysql client) |
| Auto-detected primary key is not your business key | Heuristic could not infer it | Use the step-by-step path and pass `openbkn bkn object-type create ... --primary-key ... --display-key ...` |
| `bkn search` returns `HTTP 500` | The view does not support full-text search | Switch the query `condition` from `match` to `like` |

## 6. See also

- References: [BKN Engine](../manual/bkn.md) · [Data Source Management](../manual/datasource.md) · [Quick start](../quick-start.md)
- End-to-end sample project: [`examples/02-csv-to-kn/`](../../../examples/02-csv-to-kn/) in the repo
