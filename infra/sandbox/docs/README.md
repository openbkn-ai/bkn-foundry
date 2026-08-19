# Documentation

Project documents are organized into product, design, API, development, and operations categories to avoid mixing requirements, architecture, and runbooks in the root directory.

## Structure

```text
docs/
├── README.md
├── product/
│   ├── roadmap.md
│   ├── prd/
│   └── use-cases/
├── design/
│   ├── architecture/
│   ├── modules/
│   ├── features/
│   ├── decisions/
│   └── archive/
├── api/
│   ├── rest/
│   ├── grpc/
│   └── websocket/
├── dev/
├── ops/
└── assets/
```

## Navigation

### Product

- [Product roadmap](./product/roadmap.md)
- [PRD template](./product/prd/template.md)
- [Session Python Dependency Management PRD](./product/prd/session-python-dependency-management.md)
- [Scenario: PTC data analysis and contextual Q&A](./product/use-cases/ptc-data-analysis-and-context-qa.md)
- [Background: unified sandbox temporary area](./product/use-cases/unified-sandbox-background.md)

### Design

- [Architecture overview](./design/architecture/overview.md)
- [System context](./design/architecture/system-context.md)
- [Logical architecture and flow](./design/architecture/logical-architecture.md)
- [Security and performance](./design/architecture/security-and-performance.md)
- [Storage architecture](./design/architecture/storage-architecture.md)
- [Module design: Control Plane](./design/modules/control-plane.md)
- [Module design: Container Scheduler](./design/modules/container-scheduler.md)
- [Module design: Executor](./design/modules/executor.md)
- [Module design: Python dependencies](./design/modules/python-dependencies.md)
- [Requirement design: Session Python dependency management](./design/features/session-python-dependency-management.md)
- [Requirement design: MCP Server](./design/features/mcp-server-implementation.md)
- [Design template](./design/features/template.md)
- [ADR template](./design/decisions/template.md)

### API

- [API overview](./api/README.md)
- [REST OpenAPI](./api/rest/sandbox-openapi.json)
- [Execute Sync OpenAPI](./api/rest/execute-sync-openapi.yaml)

### Dev

- [Environment setup](./dev/setup.md)
- [build](./dev/build.md)
- [Test](./dev/test.md)
- [Release](./dev/release.md)
- [Contribution guide](./dev/contributing.md)
- [Project structure](./dev/project-structure.md)

### Ops

- [deployment](./ops/deploy.md)
- [configuration](./ops/config.md)
- [Monitoring](./ops/monitoring.md)
- [Troubleshooting](./ops/troubleshooting.md)
- [Historical document: monitoring and deployment](./ops/monitoring-and-deployment.md)
- [Historical document: Runtime Node registration](./ops/runtime-node-registration.md)

## PRD <-> Design Linking Convention

Each requirement generates two documents by default:

1. `docs/product/prd/<feature>.md`
2. `docs/design/features/<feature>.md`

Both documents must use the same `<feature>` slug and link to each other.

### Required PRD section

```md
## Related design

- Design: [<title>](../../design/features/<feature>.md)
```

### Required Design section

```md
## Related requirement

- PRD: [<title>](../../product/prd/<feature>.md)
```

Copy directly from the template when creating documents to avoid adding links later.
