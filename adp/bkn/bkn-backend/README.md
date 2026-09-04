# BKN Backend

[中文](README.zh.md) | English

`bkn-backend` is the BKN Engine modeling and management service. It owns the
metadata model for Business Knowledge Networks and exposes APIs for creating,
validating, updating, searching, importing, and exporting BKN definitions.

## Current Capability Scope

Implemented capabilities:

- Knowledge network create, list, get, update, delete, name lookup, and validation.
- Object type management, including data properties, logic properties, validation,
  sample data lookup, and selected data-property updates.
- Relation type management and relation-type path lookup.
- Action type management, validation, and concept search.
- Concept group management, including nested concepts and object-type membership.
- Metric definition management and validation.
- Risk type management.
- Action schedule management, including cron validation, status update, and next
  run-time calculation.
- Resource listing through VEGA integration.
- BKN package upload/download for import/export.
- Concept indexing and search through OpenSearch and model-factory embeddings.
- BKN Trace outbox inspection plus retry/abandon operations.

Important boundaries:

- Branch is a supported data dimension through the `branch` query parameter, but
  this service does not expose a complete branch lifecycle API such as create,
  merge, publish, or rollback.
- Fine-grained authorization covers the knowledge-network root and its six child
  resource types. Child decisions use canonical `{kn_id}/{child_id}` IDs; list
  and search results are filtered before total and pagination are computed.
- Natural-language query planning and context loading are not handled here; use
  `adp/context-loader` for that layer.
- Object sample-data and resource operations depend on VEGA availability.
- Vector search and concept indexing depend on OpenSearch and model-factory
  embedding configuration.

## Main API Groups

Public API prefix:

```text
/api/bkn-backend/v1
```

Internal API prefix:

```text
/api/bkn-backend/in/v1
```

Representative public routes are registered in `server/driveradapters/routers.go`:

| Resource | Routes |
| --- | --- |
| Knowledge networks | `/knowledge-networks`, `/knowledge-networks/{kn_id}`, `/knowledge-networks/{kn_id}/validation` |
| Concept groups | `/knowledge-networks/{kn_id}/concept-groups` |
| Object types | `/knowledge-networks/{kn_id}/object-types` |
| Relation types | `/knowledge-networks/{kn_id}/relation-types`, `/knowledge-networks/{kn_id}/relation-type-paths` |
| Action types | `/knowledge-networks/{kn_id}/action-types` |
| Metrics | `/knowledge-networks/{kn_id}/metrics` |
| Risk types | `/knowledge-networks/{kn_id}/risk-types` |
| Action schedules | `/knowledge-networks/{kn_id}/action-schedules` |
| BKN import/export | `/bkns`, `/bkns/{kn_id}` |
| Resources | `/resources` |
| Trace outbox | `/api/bkn-backend/v1/trace/outbox` |

The canonical OpenAPI source is maintained at the repository root:

```text
docs/api/bkn/*.yaml
```

Run from the repository root:

```bash
npm install
make api-docs-lint
make api-docs-html
```

## Architecture

```text
bkn-backend/
  server/
    common/              # Shared helpers, settings, condition handling, trace helpers
    config/              # Service configuration
    drivenadapters/      # DB, OpenSearch, VEGA, model-factory, bkn-safe clients
    driveradapters/      # HTTP handlers, routers, request validation
    errors/              # BKN Backend error definitions
    interfaces/          # DTOs and service interfaces
    locale/              # i18n resources
    logics/              # Business logic
    version/             # Build/version metadata
    worker/              # Background concept sync and job execution
    bkn-specification/   # BKN package parsing and serialization
```

Core logic packages:

| Package | Responsibility |
| --- | --- |
| `knowledge_network` | Full BKN lifecycle orchestration |
| `object_type` | Object type validation, CRUD, indexing, sample data |
| `relation_type` | Relation type validation, CRUD, and path support |
| `action_type` | Action type validation, CRUD, search, and source checks |
| `concept_group` | Concept group tree and membership management |
| `metric` | Metric definition validation, CRUD, and search |
| `risk_type` | Risk type CRUD and concept search |
| `action_schedule` | Scheduled action metadata and cron handling |
| `permission` | bkn-safe decisions, child filtering, and ResourceParent lifecycle |
| `bkn` | BKN import/export service |

## Local Development

Prerequisites:

- Go 1.25+
- MariaDB 11.4+ or DM8
- OpenSearch 2.x
- Reachable VEGA, model-factory, and bkn-safe dependencies when
  exercising integration paths.

Configure local settings in:

```text
server/config/bkn-backend-config.yaml
```

### Authorization configuration

When `AUTH_ENABLED=true`, `BKN_SAFE_URL` is mandatory and must be an absolute
HTTP(S) URL with a host. Credentials, query strings, and fragments are rejected.
The process exits during startup when this contract is not satisfied; there is
no legacy provider or unauthenticated fallback.

The Helm values are:

| Value | Default | Meaning |
| --- | --- | --- |
| `bknSafe.url` | `http://bkn-safe:3000` | bkn-safe service root |
| `auth.knChildResourcePepEnabled` | `false` | Enable canonical child-resource PEP after migration |
| `auth.actionExecutionPepEnabled` | `false` | Enable schedule/action execution rechecks after migration |
| `auth.knChildResourceFilterChunkSize` | `0` | Optional caller-side chunk size; `0` sends one request and is not a server limit |

The authorization resource and operation contract, including error behavior,
is documented in
[`docs/api/knowledge-network-authorization.md`](../../../docs/api/knowledge-network-authorization.md).

Run locally:

```bash
cd adp/bkn/bkn-backend/server
go mod download
go run main.go
```

Default port:

```text
http://localhost:13014
```

Health check:

```bash
curl http://localhost:13014/api/bkn-backend/v1/health
```

## Testing

Use the module Makefile:

```bash
cd adp/bkn/bkn-backend
make test
make test-cover
make lint
make ci
```

The Makefile sets `I18N_MODE_UT=true` for unit tests. Coverage artifacts are
written to:

```text
adp/bkn/bkn-backend/test-result/
```

Useful direct package test example:

```bash
cd adp/bkn/bkn-backend/server
I18N_MODE_UT=true go test ./logics/object_type/... -v
```

Integration tests that require external services must stay isolated behind
explicit environment variables or build tags and must not be added to the default
`make test` path.

## Build and Deploy

Build the service binary:

```bash
cd adp/bkn/bkn-backend/server
go build -o bkn-backend .
```

Build the Docker image:

```bash
cd adp/bkn/bkn-backend
docker build -t bkn-backend:latest -f docker/Dockerfile .
```

Helm chart:

```text
adp/bkn/bkn-backend/helm/bkn-backend/
```

The normal product deployment path is the repository-level installer under
`deploy/`, which installs this service together with its dependencies.

## Maintainer Checklist

- Keep `server/driveradapters/routers.go` and `docs/api/bkn/*.yaml` in sync.
- Keep `README.md` focused on implemented service behavior, not UI or product
  capabilities owned by other repositories.
- Update tests when changing validation, import/export, indexing, or API behavior.
- Treat permission, branch lifecycle, and model/index compatibility changes as
  cross-service changes and document their impact explicitly.
