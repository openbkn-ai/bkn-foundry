# migrations — Database Initialization and Upgrade Scripts

English | [中文](README.zh.md)

This directory is the single execution source for database scripts of OpenBKN
services. When the [data-migrator](../data-migrator/) image is built,
`migrations/` is copied unchanged to `/app/repos` in the image. At runtime,
scripts are discovered and executed by service name, database type, and version.

## Directory layout

```
migrations/
├── README.md
├── README.zh.md
└── <service-key>/
    └── <database-type>/
        └── <version>/
            ├── init.sql        # Complete schema snapshot for this version; required
            └── NN-*.sql        # Numbered incremental scripts; optional
```

- **service key**: Must match a `services` key in `data-migrator/config.monorepo.yaml`.
- **database type**: Directory names are lowercase; currently managed scripts use `mariadb`.
- **version**: A dotted numeric version, such as `0.1.0`, `0.1.5`, or `1.0.0`.
- **`init.sql`**: A complete initialization snapshot for the version.
- **`NN-*.sql`**: Incremental scripts executed in ascending two-digit `NN` order within a version.

## Execution rules

### Initial installation

data-migrator executes `init.sql` from the highest version directory and records
that version as installed. Every version directory must therefore provide a
complete `init.sql` that can initialize the target database independently.

### Version upgrades

For an existing installation, data-migrator skips every `init.sql` and executes
incremental scripts for every version above the installed version, ordered first
by version and then by filename number. It records progress after each successful
script so a failed run can resume from the most recently successful script.

When adding a version, all of the following are required:

1. Add `<version>/init.sql` with the complete schema snapshot for that version.
2. Add the `NN-*.sql` scripts required to upgrade from the preceding version.
3. Retain all existing versions and scripts so deployed environments keep a valid
   upgrade path.

## Script requirements

- Every version directory must contain a non-empty `init.sql`; data-migrator lint
  fails when it is missing.
- SQL should be repeatable where possible, for example with `CREATE TABLE IF NOT EXISTS`
  and `INSERT ... ON DUPLICATE ...`.
- Every script explicitly uses `USE openbkn;`; all currently managed services use
  the `openbkn` database.
- `NN` is a two-digit decimal sequence from `01` through `99` and must be unique
  within a version.
- SQL files must retain their applicable license headers: newly created OpenBKN
  files use the OpenBKN License, while retained upstream files keep their Apache
  2.0 license headers. See the repository-root `LICENSE` and
  `LICENSE-OPENBKN.txt`.

## Managed services

| service key | Target database | Module path |
| --- | --- | --- |
| `bkn-backend` | `openbkn` | `adp/bkn/bkn-backend` |
| `bkn-backend-trace-outbox` | `openbkn` | `adp/bkn/bkn-backend` |
| `ontology-query-trace-outbox` | `openbkn` | `adp/bkn/ontology-query` |
| `vega-backend` | `openbkn` | `vega/vega-backend` |
| `agent-operator-integration` | `openbkn` | `adp/execution-factory/operator-integration` |
| `mf-model-manager` | `openbkn` | `infra/mf-model-manager` |
| `bkn-agent` | `openbkn` | `infra/bkn-agent` |
| `oss-gateway-backend` | `openbkn` | `infra/oss-gateway-backend` |
| `sandbox` | `openbkn` | `infra/sandbox` |

`sandbox_control_plane` uses an independent Python migrator (a single `.py` file
rather than the `<database-type>/<version>/init.sql` layout) and is not managed
by data-migrator.
