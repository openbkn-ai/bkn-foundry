# 05 · Skill Routing Loop — KN-driven Skill Routing

> [中文版](./README.zh.md)

> 3 materials trigger the same critical alert; `find_skills` routes each one to a
> different handling Skill — each route justified by the knowledge network, with
> no agent and no LLM in the loop.

## The Story

Continuing from example 03's procurement engineer: she now sees the disposition
plan already chosen on each alert. Three materials. Three paths. Zero prompts
edited. The `applicable_skill` relation in the business knowledge network is the
single source of truth — for each material, `find_skills` returns exactly the
Skill bound to it, and the loop executes that Skill. No reasoning, no LLM, no
agent: the routing is the data.

## What this shows

`find_skills` is **deterministic skill routing**: given a material instance, it
recalls the Skill(s) the knowledge network binds to it via `applicable_skill`.
Five components co-operate in a verifiable end-to-end loop:

| Component | Role |
|---|---|
| **execution-factory** | registers and versions the 3 Skill packages |
| **business knowledge network (BKN)** | binds Skills to materials via `applicable_skill` |
| **Vega** | maps BKN ObjectTypes to MySQL tables (read-mostly) |
| **context-loader (`find_skills`)** | routes each material instance to its bound Skill |
| **run.sh verifier** | asserts the `find_skills` route, executes the Skill, checks logs |

The same `find_skills` capability is also registered as an MCP server (via
agent-operator-integration), so any MCP client — an agent, a workflow, or this
script — can consume the identical routing. Here run.sh calls it directly over
its REST route (`POST /api/agent-retrieval/v1/kn/find_skills`) so the demo needs
no LLM.

## Prerequisites

- `openbkn` CLI (`npm install -g @openbkn/bkn-sdk`, Node ≥ 22)
- BKN Foundry with **execution-factory + Vega + context-loader** enabled
  (use `openbkn auth login <platform-url> [--insecure]` first)
- A MySQL instance reachable from the BKN Foundry (NOT from your laptop)
  with CREATE/INSERT/SELECT/UPDATE on a chosen database
- `python3` (Flask + mysql-connector-python — install via
  `pip install -r tool_backend/requirements.txt`)

Quick self-check that platform components are reachable:

```bash
openbkn auth whoami                                      # logged in?
openbkn call /api/agent-operator-integration/v1/mcp/     # execution-factory reachable?
openbkn call /api/vega-backend/v1/catalogs                # Vega reachable?
```

## Quick Start

```bash
cd examples/05-skill-routing-loop
cp env.sample .env
vim .env                                    # fill PLATFORM_HOST, DB_*
pip install -r tool_backend/requirements.txt
./run.sh                                    # ~3 minutes end-to-end
./run.sh --bonus                            # also run the Bonus segment with verification
```

> **Concurrency caveat:** Do not run two instances of `./run.sh` concurrently.
> The script uses a fixed `KN_ID` (`ex05_skill_routing`) AND fixed Skill names
> (`standard_replenish` / `substitute_swap` / `supplier_expedite`); a second
> run will collide on Skill registration and the cleanup of either run will
> delete the other run's KN.

## What you will see

| Material | KN evidence | find_skills routes to | Why |
|---|---|---|---|
| MAT-001 | binds to `substitute_swap`; SUB-001A/B in stock | substitute_swap | Python scorer ranks substitutes; calls MES |
| MAT-002 | binds to `supplier_expedite`; SUP-2 capability=expedite | supplier_expedite | Supplier can rush — POST to supplier portal |
| MAT-003 | binds to `standard_replenish` only | standard_replenish | Default path — issue PO via ERP |

For each material the script calls `find_skills`, prints the routed
`skill_id → name`, asserts it matches the expected route, then executes that
Skill against the local mock business backend and checks `.tool_backend.log`
for:

```text
[mes/swap]
[supplier/expedite]
[procurement]
```

Seeing `✓ mock backend observed MES, supplier, and ERP calls` means all three
business actions reached the mock backend.

If you also want `builtin_skill_execute_script` in the platform sandbox to hit
the mock backend directly, set `TOOL_BACKEND_PUBLIC_URL` in `.env` to an address
reachable from the platform/sandbox, such as `http://<host>:8765` on an internal
network. The default `http://127.0.0.1:8765` is only guaranteed for the local
verifier; `127.0.0.1` inside the platform sandbox is not your laptop.

## Bonus — change business data → routing follows

Run `./run.sh --bonus`. The script POSTs to the mock business backend's admin
endpoint to re-bind MAT-002 from `supplier_expedite` to the newly registered
`standard_replenish` Skill ID. This updates `materials.bound_skill_id` in
MySQL, which drives the `applicable_skill` direct-mapping FK. It then re-routes
MAT-002: the next `find_skills` call returns the new candidate set and the route
switches to `standard_replenish` — without any prompt edit or redeploy.

> **Why no rebuild step:** every object type here binds to a Vega **resource**,
> and this example never builds a local index for those resources — so ontology
> queries hit the source database on each call, and a MySQL UPDATE is visible to
> the next `find_skills` immediately. There is no KN-level build to run either;
> that API was retired.
>
> Note the flip side: building a resource index (`openbkn vega dataset build`,
> as examples 01/02 do) switches that resource's reads over to the build
> snapshot. Doing that here would break exactly this scenario — the re-binding
> would stay invisible until the next build.

## How it works (deeper read)

See [`docs/superpowers/specs/2026-04-27-skill-routing-loop-example-design.md`](../../docs/superpowers/specs/2026-04-27-skill-routing-loop-example-design.md)
for the full design including:
- BKN schema and the `applicable_skill` direct-mapping FK
- Why MCP server registration must include `X-Kn-ID` header
- Why the script registers Skills first, then renders CSVs with real Skill IDs
- The 3-step state machine for cleaning up MCPs and Skills

## Troubleshooting

If `find_skills` returns no skill (or the wrong one) for a material, the
`skills.skill_id` / `materials.bound_skill_id` values in BKN are not aligned with
the real Skill IDs returned by execution-factory. The script registers Skills
first, then renders CSVs with those real IDs, so this should not happen in a
healthy run.

## Cleanup

Platform resources (KN, MCP, Skills, Catalog) are **kept** by default; their IDs are
printed on exit. Run with `CLEANUP=1 ./run.sh` to delete them on exit, success or failure.

The local mock tool backend process is stopped on exit as well — `DEBUG_KEEP=1` keeps it
running so the whole routing loop stays callable for debugging. `CLEANUP=1` wins over
`DEBUG_KEEP=1`: an explicit cleanup always stops the backend.

Re-running over the resources a previous run left behind works: the KN id is fixed
(`ex05_skill_routing`) and `bkn push` imports in overwrite mode, so the network is
updated in place. Skills and the MCP server carry the run timestamp in their names —
a Skill name already held by a *published* Skill is rejected at publish time — so each
run registers its own set rather than colliding with the last one. Delete the old ones
by hand (or run once with `CLEANUP=1`) when the list gets noisy.
