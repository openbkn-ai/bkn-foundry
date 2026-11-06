# Workshop

[中文](README.zh.md) | English

Workshop is OpenBKN's app workshop service: apps and their published versions, the builder's AI conversations, events and tasks, and the graph explorer's analysis canvas are all kept here. The service is paid; its business implementation lives in the private openbkn-ee repository (image `workshop-ee`). This directory is the community skeleton.

## What is here

- The process skeleton (`server/app`): it loads configuration, connects the database and installs the licence gate; the paid build assembles the business routes with `RegisterRoutes` between `Boot` and `Run`.
- The shared HTTP layer (`server/driveradapters`): `/api/workshop/v1/health`, `/api/workshop/v1/ready` (the database answers) and the internal `/api/workshop/in/v1/health`; `Authenticate` (verifies the token with Hydra) and `RequireEdition` (below the tier, answers the community binary's 404) for the business routes.
- No database migrations: the paid image creates and upgrades Workshop's tables itself at startup. The skeleton creates none.

No image or Helm chart is published from here: the paid image `workshop-ee` and its chart are built in openbkn-ee, whose installer deploys Workshop as an enterprise-only service. Built on its own, the skeleton serves only the probes above; every business route answers 404.

## Configuration

- `config/workshop-config.yaml`; dependencies under `depServices`: `rds`, `hydra-admin`, `bkn-agent`, `bkn-backend` (knowledge network events).
- `BKN_SAFE_URL`: the bkn-safe base URL, required.
- Port 13022. The public `/api/workshop/v1` is routed by the Ingress; the internal `/api/workshop/in/v1` stays in the cluster.

## Development

```bash
cd workshop
make test
```
