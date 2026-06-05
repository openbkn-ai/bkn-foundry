# Execution Factory Tests (OpenBKN)

Canonical test location for Execution Factory migrated from KWeaver DIP/Core.

## Test tiers

| Tier | What | When | CI |
|------|------|------|-----|
| **L1** | bkn-studio vitest mocks | Every PR | `bkn-studio` `ci-execution-factory.yml` |
| **L2** | `openbkn-smoke` pytest | Backend up locally / manual | `bkn-foundry` collect-only on PR; live via `workflow_dispatch` |
| **L3** | Full Agent AT (`data-operator-hub`) | KWeaver platform env | Not in git; sync locally (below) |

```
adp/execution-factory/
|-- tests/                          # Agent AT (pytest) -- this directory
|   |-- testcases/
|   |   |-- data-operator-hub/      # L3 -- gitignored, sync from KWeaver
|   |   `-- openbkn-smoke/          # L2 -- tracked in git
|   |-- config/env.openbkn.example.ini
|   |-- requirements/ci-smoke.txt   # minimal deps for CI collect
|   `-- scripts/
|       |-- run-openbkn-smoke.ps1
|       `-- sync-agent-at.ps1
`-- operator-integration/
    `-- server/tests/               # HTTP files, Python CLI, Go smoke
```

## Sync full Agent AT (L3, local only)

Bulk payloads are **not** in git. Copy from a local KWeaver clone:

```powershell
cd bkn-foundry/bkn-foundry/adp/execution-factory/tests
# optional: $env:KEWEAVER_ROOT = "e:\00_code_workspace\keweaver"
.\scripts\sync-agent-at.ps1
# dry run: .\scripts\sync-agent-at.ps1 -DryRun
```

Source: `keweaver/adp/execution-factory/tests` (or `$KEWEAVER_ROOT/adp/execution-factory/tests`).

## L2 -- OpenBKN backend smoke

### Prerequisites

1. Start `agent-operator-integration` (default `http://127.0.0.1:9000`)
2. Python 3.10+ with `pytest`, `requests`, `pyyaml`
3. For token mode: Bearer token with `x-business-domain: bd_public`

### Start backend locally (AUTH_ENABLED=false)

Requires local MySQL (`dip_data_operator_hub` schema) and Redis on `127.0.0.1:6379`.

```powershell
cd bkn-foundry/bkn-foundry/adp/execution-factory/operator-integration
$env:OPENBKN_DB_PASSWORD = "<mysql-password>"
.\scripts\run-local-dev.ps1
```

Apply migrations first: `operator-integration/migrations/mariadb/`.

### Run openbkn-smoke

With Bearer token (production-like):

```powershell
cd bkn-foundry/bkn-foundry/adp/execution-factory/tests
copy config\env.openbkn.example.ini config\env.ini

$env:OPENBKN_TOKEN = "<your-bearer-token>"
$env:OPENBKN_BUSINESS_DOMAIN = "bd_public"

.\scripts\run-openbkn-smoke.ps1
```

Local dev without token (`AUTH_ENABLED=false` on backend):

```powershell
.\scripts\run-openbkn-smoke.ps1 -AuthDisabled
```

Or manually:

```powershell
py -m pytest testcases/openbkn-smoke --confcutdir=testcases/openbkn-smoke -q
```

### HTTP / CLI / Go

- REST Client: `operator-integration/server/tests/http/` (`env.http`, `operator.http`, ...)
- Python CLI: `operator-integration/server/tests/tool/operator_client.py`
- Go tests: `cd operator-integration && ./project.sh -t`

## L3 -- Full Agent AT

After `sync-agent-at.ps1`, requires Hydra, `eisoo`, and MySQL `adp` schema:

```powershell
pip install -r requirements/requirements.txt
py -m pytest testcases/data-operator-hub/api/operator/test_get_operator_category.py -q
```

Use `--confcutdir=testcases/openbkn-smoke` only for L2; full suite loads root `conftest.py`.

## L1 -- bkn-studio frontend

```bash
cd bkn-studio/bkn-studio
pnpm test:execution-factory
```

## CI secrets (optional L2 live smoke)

Repository secrets for `workflow_dispatch` with `run_live_smoke=true`:

| Secret | Purpose |
|--------|---------|
| `OPENBKN_TOKEN` | Bearer token when auth enabled |
| `OPENBKN_AUTH_ENABLED` | Set to `false` for AUTH_ENABLED=false backends |
| `OPENBKN_BUSINESS_DOMAIN` | Optional; defaults to `bd_public` in the workflow |

## bkn-studio mirror

`bkn-studio/bkn-studio/tests/execution-factory/` is a convenience mirror. **Canonical backend + smoke path is this directory.**
