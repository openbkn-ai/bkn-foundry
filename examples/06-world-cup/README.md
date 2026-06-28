# 06 · World Cup database → Vega Catalog → BKN → Vega-SQL tool

> Load the public [Fjelstul World Cup Database](https://github.com/jfjelstul/worldcup) into MySQL `wc_*` tables, then run a single script **`./run.sh`** that scans the source through **Vega**, pushes a checked-in **BKN** (`worldcup_vega_catalog_bkn`), builds search indexes, and registers a published **`vega_sql_execute`** tool you can query directly against the 27 tables.

[中文版](./README.zh.md)

## The path

```
                       ┌─ 1) Download CSVs   fetch 27 CSVs from jfjelstul/worldcup (cached)
                       │
                       ├─ 2) Import MySQL    openbkn ds connect + ds import-csv → wc_* tables
                       │                     (pre-creates wc_matches / wc_team_appearances
                       │                      with VARCHAR(255) to dodge MySQL Error 1118)
                       │
                       ├─ 3) Vega scan       vega catalog create + discover --wait
                       │
   ./run.sh  ─────────►├─ 4) Render BKN      map vega Resources → render worldcup-bkn.tar
                       │
                       ├─ 5) Push BKN +      bkn validate + push (idempotent),
                       │   build indexes     then build vega resource OpenSearch datasets
                       │                     (7 entity tables get vector embedding)
                       │
                       └─ 6) Upload toolbox  openbkn toolbox create + tool upload <OpenAPI>
                                             (registers + publishes `vega_sql_execute`
                                              so you can run raw SQL against the wc_* tables)
```

The pipeline ends here: a Vega catalog **BKN** (`worldcup_vega_catalog_bkn`) backed by a published, queryable **`vega_sql_execute`** tool over the 27 `wc_*` MySQL tables.

Checked-in assets in this directory:
- **`worldcup-bkn.tar`** — offline BKN tree (27 object types, 29 `rel_*` edges) packaged as a tar archive; each OT ends with **`resource | {{*_RES_ID}}`** placeholders. `network.bkn` pins id `worldcup_vega_catalog_bkn`. `run.sh` extracts to `.tmp/worldcup-bkn/` before rendering.
- **`vega_sql_execute.openapi.json`** — OpenAPI 3.0 spec for the SQL-execute tool. Step 6 uploads it via `openbkn tool upload` (the OpenAPI parser path; sidesteps the 0.7.0 `openbkn toolbox import` bug that stored `api_spec` as null).
- **`bkn-network-structure.html`** — single-file visual overview of the BKN: the 4 concept groups, all 27 OTs (dashed = no FK in minimal mode), the matches/tournaments hubs, and the full 29-row relation table. Open in any browser; no build step.

## Data source and license

CSVs come from Joshua C. Fjelstul’s **The Fjelstul World Cup Database** ([repo](https://github.com/jfjelstul/worldcup)).
- **© 2023 Joshua C. Fjelstul, Ph.D.**
- Licensed under **CC-BY-SA 4.0** — [legal text](https://creativecommons.org/licenses/by-sa/4.0/legalcode)

Keep attribution and the share-alike notice on derived data. **Pin a revision** via `WORLDCUP_REF` in `.env` (default `master`, which may move).

## First-time setup checklist

`run.sh` only automates the 6 steps above. On a fresh machine + fresh cluster you still need these one-shot platform tasks:

1. **Install the BKN Foundry platform** (k8s + bkn-backend + ontology-query + vega-backend + mf-model-* + opensearch + minio + mariadb). Use `deploy/onboard.sh` from the repo root — see [deploy/README.md](../../deploy/README.md). Recommended `0.8.0+` (fixes the `_score` resource-path bug and the toolbox-import write bug).
2. **Authenticate the CLI**: `openbkn auth login https://<your-platform-url>` (writes `~/.bkn/`).
3. **Register an embedding model** (vector index needs it; keyword-only build works without it):
   ```bash
   openbkn model small add --name text-embedding-v4-cn \
     --type embedding --batch-size 10 --max-tokens 512 --embedding-dim 1024 \
     --model-config-file <emb.json>
   ```
   `EMBEDDING_MODEL_NAME` in `.env` is resolved to `model_id` at runtime (default `text-embedding-v4-cn`).
4. **Wire BKN to the embedding model** (only needed for KN-level semantic search; this example doesn't rely on it):
   ```bash
   sudo bash deploy/onboard.sh --enable-bkn-search \
     --bkn-embedding-name=text-embedding-v4-cn
   ```
5. **Stage MySQL** — create the `worldcup` database and a user that is reachable from the openbkn platform (k8s pod network). Fill `DB_HOST / DB_PORT / DB_NAME / DB_USER / DB_PASS` in `.env`.

Once those are done, `./run.sh` is fully turnkey and idempotent.

## Prerequisites (per-shell)

```bash
npm install -g @openbkn/bkn-sdk
openbkn auth login https://<your-platform-url>
# Use the Node SDK `openbkn` (avoid a broken /usr/local/bin/openbkn stub).
# MySQL must be reachable from the platform AND from Vega connectors.
# curl + jq + python3 + (optional) `mysql` CLI for the wide-table pre-create
```

## Quick start

```bash
cd examples/06-world-cup
cp env.sample .env
vim .env   # at minimum: DB_*

# Single command runs all 6 steps end-to-end; every step is idempotent on rerun.
./run.sh
```

`./run.sh --help` lists every flag. Common variants:

| Command | Effect |
|---------|--------|
| `./run.sh` | Run steps 1→6 |
| `./run.sh --dry-run` | Plan only, no API calls |
| `./run.sh --from 3` | Rerun from Vega scan onward (CSVs already in MySQL) |
| `./run.sh --only 5` | Only run step 5 (BKN push + index build) |
| `./run.sh --only 6` | Only run toolbox create + tool upload + publish |

## The registered `vega_sql_execute` tool

Step 6 publishes one OpenAPI-described tool, `vega_sql_execute`, that runs raw MySQL SQL against the Vega resources behind the `worldcup_vega_catalog_bkn` BKN:

| Tool | Source toolbox | Use when |
|---|---|---|
| **`vega_sql_execute`** | uploaded by step 6 from `vega_sql_execute.openapi.json` | Raw MySQL SQL — `SELECT`, `WHERE`, `ORDER BY`, `GROUP BY`, multi-table `JOIN`, `COUNT(*)`. Reference resources by `{{<resource_id>}}` placeholder; `resource_id` comes from `openbkn vega resource list`. |

The platform built-in `search_schema` / `query_object_instance` / `query_instance_subgraph` tools also remain available against the same KN for schema exploration and equality/range lookups over the OpenSearch datasets built in step 5.

## Example queries

Once `./run.sh` finishes, run the published `vega_sql_execute` tool. Resolve a table's `resource_id` first, then reference it as `{{<resource_id>}}` in the SQL:

```bash
# list table resources to grab a resource_id
openbkn vega resource list --datasource-id <catalog_id> --type table

# run SQL through the published tool (TOOLBOX_BOX_ID / VEGA_TOOL_ID are echoed by step 6)
openbkn tool invoke <VEGA_TOOL_ID> --toolbox <TOOLBOX_BOX_ID> \
  --input query='<your SQL with {{<resource_id>}} placeholders>' \
  --input resource_type=mysql
```

### Q1 · Messi's World Cup awards
**SQL**: `SELECT tournament_name, award_name FROM {{<award_winners_resource_id>}} WHERE family_name='Messi' AND given_name='Lionel'` → returns:
- 2014 FIFA Men's World Cup — **Golden Ball**
- 2022 FIFA Men's World Cup — **Golden Ball** + **Silver Boot**

### Q2 · Sun Wen's per-tournament goals + all-time rank
**SQL**: `SELECT tournament_name, COUNT(*) FROM {{<goals_resource_id>}} WHERE family_name='Sun' AND given_name='Wen' GROUP BY tournament_name` (+ a second SQL for the ranking) → returns:

| Tournament | Goals |
|---|---|
| 1991 Women's WC | 1 |
| 1995 Women's WC | 2 |
| **1999 Women's WC** | **7** (also won Golden Ball + Golden Boot) |
| 2003 Women's WC | 1 |
| **Total** | **11** (tied 5th on all-time women's WC scorers) |

### Q3 · Last three men's World Cup winners
**SQL**: `SELECT year, host_country, winner FROM {{<tournaments_resource_id>}} WHERE tournament_name LIKE '%Men%' ORDER BY CAST(year AS UNSIGNED) DESC LIMIT 3` → returns:
- 2022 Qatar → Argentina
- 2018 Russia → France
- 2014 Brazil → Germany

Every number comes straight from the 27 `wc_*` tables you imported in step 2 — exact, no approximation.

## The 27 datasets (grouped)

1. **Core entities** — `tournaments`, `confederations`, `teams`, `players`, `managers`, `referees`, `stadiums`, `matches`, `awards`
2. **Tournament mappings** — `qualified_teams`, `squads`, `manager_appointments`, `referee_appointments`
3. **Match appearances** — `team_appearances`, `player_appearances`, `manager_appearances`, `referee_appearances`
4. **In-match events** — `goals`, `penalty_kicks`, `bookings`, `substitutions`
5. **Standings / awards** — `host_countries`, `tournament_stages`, `groups`, `group_standings`, `tournament_standings`, `award_winners`

## Troubleshooting

| Symptom | What to try |
|---------|--------------|
| Step 1 download fails | Check network; verify `WORLDCUP_REF` points at a revision with `data-csv/`. |
| `openbkn auth` 401 | `openbkn auth login`; confirm business domain via `openbkn config show`. |
| `import-csv` → MySQL **Error 1118** | Step 2 pre-creates `wc_matches` / `wc_team_appearances` with VARCHAR(255) via the local `mysql` CLI. Without that client installed you must pre-create them manually or relax column types. |
| Vega `discover` fails | Set `VEGA_CATALOG_ID` then `./run.sh --from 4`. |
| Fewer than 27 Resources | `databases` in connector config incomplete, or discover didn't finish — adjust `VEGA_MYSQL_DATABASES` and rerun step 3. |
| Step 5 index build skipped for a large table | Expected on platform 0.7.0 — `vega-backend` has a batch-sync cursor bug on tables `>2× batch_size` (8 event tables). Those tables stay queryable via `vega_sql_execute`. Fixed in 0.8.0+. |
| Step 5 `embedding model … not registered` | Either register one (see setup checklist) or set `DO_INDEX=0` / `EMBEDDING_MODEL_NAME=` (empty) — script then builds keyword-only indexes. |
| `tool upload` / `toolbox publish` fails in step 6 | Confirm the CLI is logged in and `vega_sql_execute.openapi.json` is present; set `FORCE_TOOLBOX_REIMPORT=1` to delete + re-import a stale same-name toolbox. |

## Differences from Example 02

| | 02-csv-to-kn | 06-world-cup |
|---|--------------|--------------|
| Data | Three small HR CSVs in repo | 27 upstream CSVs (downloaded), CC-BY-SA |
| Knowledge path | `create-from-csv` | **MySQL + Vega Resource** + checked-in **`worldcup-bkn.tar`** push |
| Tooling | none | OpenAPI-uploaded `vega_sql_execute` for raw SQL over the catalog |
| Entry point | Multiple scripts | Single `./run.sh` (steps 1–6, separately runnable, idempotent) |

## Cleanup

`./run.sh` does **not** auto-delete the datasource, MySQL tables, Vega catalog, KN, or toolbox. Remove them explicitly via the `openbkn` CLI when no longer needed.
