# bkn-eval

Evaluation for agents that use BKN through Context Loader: datasets with expected facts, runs against an MCP entry point, grading, and comparison between arms.

**This is a command-line tool, not a service.** It runs locally or in CI, is not deployed, and has no database or HTTP API. When bkn-eval becomes a service, the server is added as a second command in this Go module (`cmd/bkn-eval-server`) and reuses `internal/`; the datasets and schemas here become its storage contract as they are.

Design: bkn-docs `docs/foundry/bkn-eval/design/issue-tbd-bkn-eval-module-design.md` (module) and `docs/foundry/context-loader/design/issue-1175-context-loader-mcp-token-optimization.md` §13.4 (the arms and gates this tool serves). Tracking: #272, epic #1704.

## Layout

The code follows the ports-and-adapters layout of `bkn-trace/agent-observability`, so turning the tool into a service adds adapters and leaves the domain alone.

| Path | Contents |
| --- | --- |
| `cmd/bkn-eval/` | The command-line entry. It only wires adapters together. |
| `internal/domain/` | Entities and domain services: datasets and cases now; grading, statistics and experiments later. Pure Go, no I/O. Anything that can change a score or a conclusion lives here. |
| `internal/port/` | Interfaces the domain needs from outside: dataset source now; MCP host, model client, fixture importer, Trace reader and result store later. |
| `internal/driveradapter/` | Entry adapters: `cli` now; an HTTP adapter when bkn-eval becomes a service. |
| `internal/drivenadapter/` | Outbound adapters: `filestore` now; Context Loader MCP, model factory, bkn-backend and BKN Trace clients later, and a database store for the service. |
| `schemas/` | JSON Schemas for datasets (and later runs and reports). |
| `datasets/` | Datasets, one directory each. |
| `scripts/` | Thin wrappers that only call `bkn-eval`. No scoring or statistics in scripts. |

Becoming a service therefore means a `cmd/bkn-eval-server`, an HTTP driver adapter, a database driven adapter behind the same ports, and migrations under `migrations/bkn-eval`.

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
go run ./cmd/bkn-eval validate datasets/cypher-probe/dataset.json
```

## Dataset rules

- **Public fixtures only.** This repository is public: cases may only use public fixture networks and demo data. Never add customer networks, entity names or query logs.
- **Facts, not transcripts.** A case lists the facts a correct answer must state (`contains`, `regex`, or `number` with an optional tolerance) and the tool paths that count as a correct way to get there. Paths use real target tool names; extra calls in between are allowed.
- **Solvability.** `both` cases can be answered on both MCP entries; `full_only` cases are scored on the compact entry by whether it states the boundary; `no_mcp` cases must not call tools and list no paths.
- **Holdout.** Cases with `split: holdout` are never used to tune tool descriptions, instructions or defaults, and are run once per evaluation cycle.
- **Stable IDs.** `dataset_id`, `version` and `case_id` do not change once published, so results stay comparable and can be imported into the future service.

Dependencies at run time are the public interfaces of the systems under test only: bkn-backend for fixtures, Context Loader MCP entries, the model factory (default model, empty model name), and BKN Trace for process evidence. Credentials are passed in through the environment, never stored here.
