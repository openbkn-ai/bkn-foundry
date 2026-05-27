# GitHub Actions workflows

Workflow YAML files must stay in this directory (flat layout). GitHub does not load workflows from subfolders.

## Naming convention

| Prefix | Purpose |
|--------|---------|
| `lint-` | Repo convention checks (branch names, commits, workflow naming, …) |
| `ci-` | Build, test, typecheck, and other integration checks on PR / push |
| `deploy-` | Deploy artifacts or sites |
| `release-` | Versioned builds and publishing (images, charts, …) |
| `security-` | Supply chain / app security (CodeQL, dependency review, SARIF, …) |
| `automation-` | Repo hygiene & bots (stale issues, labeler, welcome, scheduled housekeeping, …) |
| `reusable-` | **Only** [`workflow_call`](https://docs.github.com/en/actions/using-workflows/reusing-workflows) entrypoints (no direct `push`/`pull_request` unless you intentionally combine) |

**If nothing fits:** prefer folding into `ci-` (e.g. perf benchmarks) or `lint-` (policy-as-code checks). If you need a new top-level prefix, extend the allowlist in `lint-workflow-files.yml` and add a row here in the same PR.

## Index

| File | Workflow name (UI) | Trigger | Scope |
|------|-------------------|---------|--------|
| [`lint-workflow-files.yml`](./lint-workflow-files.yml) | Workflow File Naming | `pull_request` (`.github/workflows/**`) | Enforces allowed filename prefixes (see table above) |
| [`lint-branch-name.yml`](./lint-branch-name.yml) | Branch Name Lint | `pull_request` | Branch naming rules |
| [`lint-commit.yml`](./lint-commit.yml) | Commit Message Lint | `pull_request` | Commit message checks |
| [`release-agent-observability.yml`](./release-agent-observability.yml) | agent-observability-release | `push` (`trace-ai/agent-observability/**`, …), `workflow_dispatch` | Agent observability image + Helm chart |
| [`release-otelcol-chart.yaml`](./release-otelcol-chart.yaml) | otelcol-chart-release | `push` (`trace-ai/otelcol-contribute-chart/**`, …), `workflow_dispatch` | OTel collector Helm chart to GHCR |
| [`release-infra-model-factory-base.yml`](./release-infra-model-factory-base.yml) | release-infra-model-factory-base | `push` (`infra/model-factory-base/**`), `workflow_dispatch` | Shared base image for mf-model-* → GHCR (`model-factory-base:v2`) |
| [`release-infra-oss-gateway.yml`](./release-infra-oss-gateway.yml) | release-infra-oss-gateway | `push` (`infra/oss-gateway-backend/**`), `workflow_dispatch` | oss-gateway-backend image + Helm chart |
| [`release-infra-mf-model-api.yml`](./release-infra-mf-model-api.yml) | release-infra-mf-model-api | `push` (`infra/mf-model-api/**`), `workflow_dispatch` | mf-model-api image + Helm chart (base: model-factory-base) |
| [`release-infra-mf-model-manager.yml`](./release-infra-mf-model-manager.yml) | release-infra-mf-model-manager | `push` (`infra/mf-model-manager/**`), `workflow_dispatch` | mf-model-manager image + Helm chart (base: model-factory-base) |
| [`release-proton-mariadb.yml`](./release-proton-mariadb.yml) | release-proton-mariadb | `push` (`deploy/charts/proton-mariadb/**`), `workflow_dispatch` | proton-mariadb Helm chart (chart-only) → GHCR |
| [`release-trace-ai-otelcol.yml`](./release-trace-ai-otelcol.yml) | release-trace-ai-otelcol | `push` (`trace-ai/otelcol-contribute-chart/**`), `workflow_dispatch` | otelcol-contrib Helm chart (chart-only) → GHCR |

When you add a workflow, append a row here and use a `paths` filter when it should only run for part of the monorepo.
