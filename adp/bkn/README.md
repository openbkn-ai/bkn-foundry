# BKN Engine

[中文](README.zh.md) | English

`adp/bkn` contains the BKN Engine backend services for Business Knowledge Network
modeling and query. It is part of BKN Foundry and is not a standalone product UI.

The subsystem currently consists of two Go services:

| Service | Path | Default port | Responsibility |
| --- | --- | --- | --- |
| BKN Backend | `bkn-backend/` | `13014` | Knowledge network modeling, validation, import/export, concept indexing, metrics, risk types, and action schedules |
| Ontology Query | `ontology-query/` | `13018` | Object data query, property query, subgraph query, action execution, action logs, and metric data query |

## Capability Scope

Implemented and actively maintained capabilities:

- Knowledge network CRUD and validation.
- Object type, relation type, action type, concept group, metric, risk type, and action schedule management.
- BKN package import/export through the public API.
- Concept indexing and search through OpenSearch plus the configured embedding model.
- Object instance, object property, subgraph, and object-started subgraph queries.
- Action execution with asynchronous execution records, status query, logs, and cancellation.
- Metric query and dry-run execution through Ontology Query and VEGA resource data.
- Trace outbox inspection and manual retry/abandon operations for BKN Trace integration.

Known boundaries:

- Branch is supported as a model/query dimension, but this directory does not expose a complete branch lifecycle API such as create, merge, publish, or rollback.
- Fine-grained permission integration is not fully enforced in `bkn-backend`; some permission hooks are still disabled in the service layer.
- Natural-language planning and high-level context retrieval are handled by `adp/context-loader`, not by `adp/bkn` directly.
- Query execution depends on BKN metadata, VEGA resource data, OpenSearch indexes, and model-factory embedding configuration.
- Some advanced metric fields are only partially available end to end; use the OpenAPI notes and integration tests as the source of truth.

## Repository Layout

```text
adp/bkn/
  bkn-backend/       # Modeling and management service
  ontology-query/    # Query and action execution service
  AGENTS.md          # Agent collaboration rules for this subsystem
  README.md          # This file
  README.zh.md       # Chinese overview
```

Both services follow the same broad layout:

```text
server/
  common/            # Shared helpers, settings, and condition handling
  config/            # Local configuration files
  drivenadapters/    # Database, OpenSearch, model-factory, VEGA, and downstream clients
  driveradapters/    # HTTP handlers and routers
  errors/            # Service error definitions
  interfaces/        # DTOs and service interfaces
  locale/            # i18n resources
  logics/            # Business logic
  main.go            # Service entrypoint
```

`bkn-backend` also contains `worker/` for background indexing and job execution,
and `server/bkn-specification/` for BKN package parsing/import-export support.

## API Documentation

The canonical API documentation lives at the repository root:

| Service | OpenAPI source |
| --- | --- |
| BKN Backend | `docs/api/bkn/*.yaml` |
| Ontology Query | `docs/api/ontology-query/ontology-query.yaml` |

Generate or lint docs from the repository root:

```bash
npm install
make api-docs-lint
make api-docs-html
```

Public API prefixes:

```text
/api/bkn-backend/v1
/api/ontology-query/v1
```

Internal API prefixes:

```text
/api/bkn-backend/in/v1
/api/ontology-query/in/v1
```

## Local Development

Prerequisites:

- Go 1.25+
- MariaDB 11.4+ or DM8
- OpenSearch 2.x
- Running dependencies configured in each service's `server/config/*.yaml`

Run BKN Backend:

```bash
cd adp/bkn/bkn-backend/server
go mod download
go run main.go
```

Run Ontology Query:

```bash
cd adp/bkn/ontology-query/server
go mod download
go run main.go
```

Health checks:

```bash
curl http://localhost:13014/health
curl http://localhost:13018/health
```

## Testing

Each service exposes a Makefile as its standard local entrypoint.

```bash
cd adp/bkn/bkn-backend
make test
make test-cover
make lint
make ci

cd ../ontology-query
make test
make test-cover
make lint
make ci
```

Unit tests require `I18N_MODE_UT=true`; the Makefile sets it automatically.

## Deployment

The normal deployment path is through the repository-level installer and release
manifests under `deploy/`.

For service-level image/chart work:

```bash
cd adp/bkn/bkn-backend
docker build -t bkn-backend:latest -f docker/Dockerfile .

cd ../ontology-query
docker build -t ontology-query:latest -f docker/Dockerfile .
```

Helm charts are kept next to each service:

```text
adp/bkn/bkn-backend/helm/bkn-backend/
adp/bkn/ontology-query/helm/ontology-query/
```

## Notes for Maintainers

- Keep this README aligned with the actual routers and root OpenAPI files.
- Do not reintroduce links to legacy `api_doc/*.html` outputs.
- If a capability is implemented by another subsystem, name that subsystem instead
  of describing it as native to `adp/bkn`.
