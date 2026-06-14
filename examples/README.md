# BKN Foundry Examples

[中文版](./README.zh.md)

End-to-end examples that demonstrate core BKN Foundry capabilities using the CLI.

| Example | Story | What it shows |
|---------|-------|---------------|
| [01-db-to-qa](./01-db-to-qa/) | *A supply chain analyst stops waiting for DBA queries — her database answers questions directly in natural language* | MySQL → Knowledge Network → Semantic Search → Agent Chat |
| [02-csv-to-kn](./02-csv-to-kn/) | *An HR director's scattered spreadsheets become a connected knowledge network she can traverse and query* | CSV → Knowledge Network → Subgraph Traversal → Agent Q&A |
| [03-action-lifecycle](./03-action-lifecycle/) | *A procurement engineer arrives at 8am to find today's inventory alerts already generated — the knowledge network did it overnight* | CSV → Knowledge Network → Action → Schedule → Audit Log |
| [04-multi-agent-session-id](./04-multi-agent-session-id/) | *A platform feature audit shows a custom input field travels intact through father → sons → SKILL, every step verifiable* | Dolphin orchestration → Multi-Agent → Custom Input → SKILL invocation |
| [05-skill-routing-loop](./05-skill-routing-loop/) | *3 materials, 3 critical alerts, 3 different handling paths — each justified by the knowledge network* | MySQL → BKN (via Vega) → find_skills → Decision Agent → Skill → Action |
| [06-world-cup](./06-world-cup/) | *An analyst loads 27 public World Cup CSVs into MySQL, binds Vega Resources, pushes a checked-in BKN, then asks an Agent cross-table questions* | Public CSVs (CC-BY-SA) → MySQL + Vega Catalog BKN (`worldcup_vega_catalog_bkn`) → Agent |

## Getting Started

Each example is self-contained. Enter the directory, copy `env.sample` to `.env`,
fill in your credentials, and run the script:

```bash
cd 01-db-to-qa
cp env.sample .env
vim .env        # Fill in DB_HOST, DB_USER, DB_PASS, etc.
./run.sh
```

> **Security:** `.env` files are gitignored. Never commit credentials to version control.
> Each `env.sample` contains placeholder values and comments explaining what each variable does.

All examples require:
- openbkn CLI: `npm install -g @openbkn/bkn-sdk` (Node ≥ 22)
- Platform login as an admin/super-admin: `openbkn auth login https://<your-platform-url>`
  (a non-admin user cannot create catalogs / KNs — `Public.Forbidden`)
- `jq` and `python3` **≥ 3.7** on the runner (the scripts use `from __future__ import annotations`;
  CentOS/RHEL 8 ships 3.6 — install `python38` and put it first on `PATH`)
- MySQL reachable **from the runner AND from the platform/Vega pods** — set `DB_HOST` to a
  cluster-routable address (Service ClusterIP / DNS), not `127.0.0.1`
- For BKN semantic search, a registered embedding model (DashScope `text-embedding-v*` needs
  `api_url` ending in `/compatible-mode/v1/embeddings` and `batch_size <= 10`)

> Behind a restrictive network, fetch upstream CSVs via a mirror (e.g. jsDelivr
> `https://cdn.jsdelivr.net/gh/<owner>/<repo>@<ref>/<path>`) and pull images via `docker.m.daocloud.io`.

See the README inside each example for specific prerequisites.

**06-world-cup** uses a single `./run.sh` entry point (steps 1–7, separately runnable, idempotent). See its README for details.

## Cleanup

Most scripts delete all created resources (datasources, knowledge networks, actions) automatically
on exit — whether the run succeeds or fails.

The exception is `04-multi-agent-session-id`, which keeps the SKILL and three demo agents on the
platform after a successful run so they can be inspected in the Web UI; pass `--cleanup` to remove
them.

**06-world-cup** leaves datasources, MySQL rows, Vega catalogs, and the pushed KN in place unless you delete them manually.
