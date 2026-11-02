# Execution Factory Tests (OpenBKN)

Canonical test location for Execution Factory, migrated from the legacy upstream platform.

## Test tiers

| Tier | What | When | CI |
|------|------|------|-----|
| **L1** | bkn-studio vitest mocks | Every PR | `bkn-studio` `ci-execution-factory.yml` |
| **L2** | `openbkn-smoke` pytest | Backend up locally / manual | `bkn-foundry` collect-only on PR; live via `workflow_dispatch` |

```
adp/execution-factory/
|-- tests/                          # Agent AT (pytest) -- this directory
|   |-- testcases/
|   |   `-- openbkn-smoke/          # L2 -- tracked in git
|   |-- config/env.openbkn.example.ini
|   |-- requirements/ci-smoke.txt   # minimal deps for CI collect
|   `-- scripts/
|       `-- run-openbkn-smoke.ps1
`-- operator-integration/
    `-- server/tests/               # HTTP files, Python CLI, Go smoke
```

## L2 -- OpenBKN backend smoke

### Prerequisites

1. Start `agent-operator-integration` (default `http://127.0.0.1:9000`)
2. Python 3.10+ with `pytest`, `requests`, `pyyaml`
3. A valid Bearer token

### Start backend locally

Requires local MySQL (`dip_data_operator_hub` schema), Redis on `127.0.0.1:6379`, and reachable Hydra/BKN Safe dependencies configured for the service.

```powershell
cd bkn-foundry/bkn-foundry/adp/execution-factory/operator-integration
$env:OPENBKN_DB_PASSWORD = "<mysql-password>"
$env:BKN_SAFE_URL = "http://<bkn-safe-host>:3000"
.\scripts\run-local-dev.ps1
```

Apply migrations first: `operator-integration/migrations/mariadb/`.

### Run openbkn-smoke

With Bearer token (production-like):

```powershell
cd bkn-foundry/bkn-foundry/adp/execution-factory/tests
copy config\env.openbkn.example.ini config\env.ini

$env:OPENBKN_TOKEN = "<your-bearer-token>"
.\scripts\run-openbkn-smoke.ps1
```

Or manually:

```powershell
py -m pytest testcases/openbkn-smoke --confcutdir=testcases/openbkn-smoke -q
```

### HTTP / CLI / Go

- REST Client: `operator-integration/server/tests/http/` (`env.http`, `toolbox.http`, ...)
- Go tests: `cd operator-integration && ./project.sh -t`

## L1 -- bkn-studio frontend

```bash
cd bkn-studio/bkn-studio
pnpm test:execution-factory
```

## CI secrets (optional L2 live smoke)

Repository secrets for `workflow_dispatch` with `run_live_smoke=true`:

| Secret | Purpose |
|--------|---------|
| `OPENBKN_TOKEN` | Bearer token for the authenticated backend |

## bkn-studio mirror

`bkn-studio/bkn-studio/tests/execution-factory/` is a convenience mirror. **Canonical backend + smoke path is this directory.**
