# bkn-eval

Evaluation for agents that use BKN through Context Loader: datasets with expected facts, runs against an MCP entry point, grading, and comparison between arms.

**This is a command-line tool, not a service.** It runs locally or in CI, is not deployed, and has no database or HTTP API. When bkn-eval becomes a service, the server is added as a `serve` command in this Go module and reuses `src/`; the datasets and schemas here become its storage contract as they are.

Design: bkn-docs `docs/foundry/bkn-eval/design/issue-tbd-bkn-eval-module-design.md` (module) and `docs/foundry/context-loader/design/issue-1175-context-loader-mcp-token-optimization.md` §13.4 (the arms and gates this tool serves). Tracking: #272, epic #1704.

## Layout

The code follows the hexagonal layout of `bkn-trace/agent-observability` (see its `src/readme.md`), so turning the tool into a service adds adapters and leaves the domain alone.

| Path | Contents |
| --- | --- |
| `main.go` | The command-line entry. It only calls `src/boot`. |
| `src/boot/` | Assembly: builds the driven adapters and hands them to the driver adapter. |
| `src/domain/valueobject/` | Value objects, one package per concept with a `vo` suffix: `datasetvo` now; runs and reports later. Pure Go, no I/O. |
| `src/domain/service/` | Domain services with an `svc` suffix, added as they arrive: `runsvc` (the MCP host runner), `gradesvc`, `reportsvc`. Anything that can change a score or a conclusion lives in `src/domain`. |
| `src/port/driven/` | One package per outbound port with an `i` prefix: `idatasetsource` now; MCP session, model client, fixture importer, Trace reader and result store later. |
| `src/driveradapter/` | Entry adapters: `cli` now; `api/httphandler` when bkn-eval becomes a service. |
| `src/drivenadapter/` | Outbound adapters grouped by access kind: `fileaccess/datasetfile` now; `httpaccess/…` for Context Loader MCP, the model factory, bkn-backend and BKN Trace later, and `dbaccess/…` for the service. |
| `schemas/` | JSON Schemas for datasets (and later runs and reports). |
| `datasets/` | Datasets, one directory each. |
| `scripts/` | Thin wrappers that only call `bkn-eval`. No scoring or statistics in scripts. |

Becoming a service therefore means a `serve` command in `src/boot`, an HTTP driver adapter, a database driven adapter behind the same ports, and migrations under `migrations/bkn-eval`.

## Commands

| Command | Status |
| --- | --- |
| `validate <dataset.json>...` | Available. Checks datasets against the contract. |
| `fixture push` | Planned. Imports a fixture network through `POST /api/bkn-backend/v1/bkns`. |
| `run` | Planned. Acts as the MCP host against `/mcp/` or `/mcp-compact/`, calling models through the model factory. |
| `grade` | Planned. Scores answers against the dataset facts. |
| `report` | Planned. Compares arms and reports non-inferiority on the 95% confidence interval. |

```bash
make ci                              # vet, unit tests, dataset validation
go run . validate datasets/cypher-probe/dataset.json
```

## Dataset rules

- **Public fixtures only.** This repository is public: cases may only use public fixture networks and demo data. Never add customer networks, entity names or query logs.
- **Facts, not transcripts.** A case lists the facts a correct answer must state (`contains`, `regex`, or `number` with an optional tolerance) and the tool paths that count as a correct way to get there. Paths use real target tool names; extra calls in between are allowed.
- **Solvability.** `both` cases can be answered on both MCP entries; `full_only` cases are scored on the compact entry by whether it states the boundary; `no_mcp` cases must not call tools and list no paths.
- **Holdout.** Cases with `split: holdout` are never used to tune tool descriptions, instructions or defaults, and are run once per evaluation cycle.
- **Stable IDs.** `dataset_id`, `version` and `case_id` do not change once published, so results stay comparable and can be imported into the future service.

Dependencies at run time are the public interfaces of the systems under test only: bkn-backend for fixtures, Context Loader MCP entries, the model factory (default model, empty model name), and BKN Trace for process evidence. Credentials are passed in through the environment, never stored here.
