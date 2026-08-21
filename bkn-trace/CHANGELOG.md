# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Changed

- Trace Core now owns creation of `bkn_trace_ee_provenance_analyses` in the versioned MariaDB migration manifest, so Community and Enterprise images initialize the same schema before optional Enterprise routes use it.
- Renamed the module directory from `trace-ai/` to `bkn-trace/` to align with the platform-wide `bkn-*` naming (display name: BKN Trace). The Go module path changed to `github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability`; CI/release workflows, CODEOWNERS, and issue routing were updated accordingly. Image and chart names (`agent-observability`, `otelcol-contrib`) are unchanged.
- Complete OpenBKN installations persist Trace Core in the fixed `bkn_trace` MariaDB database and Evidence in OpenSearch. Offline installations mirror the Evidence index hook image into the configured offline registry; online installations keep the chart registry unless explicitly overridden.
- Managed lifecycle writes remain available through the cluster-internal `8081` Service and are now also exposed on public port `8080` for OAuth-authenticated SDK clients. Public writes derive owner identity server-side and are restricted to the deployment-approved tenant and business domain. Evidence Event/Artifact writes remain on public port `8080` and use the independent ingest token.
- The complete installer creates or validates the Evidence ingest Secret before installing releases, so Context Loader receives the token regardless of release manifest order.

### Upgrade Notes

- `X-BKN-Trace-Query-Token` query authentication and the `evidence.queryAuth.existingSecret` / `secretKey` chart values were removed. Platform queries use OAuth; custom external readers must migrate to OAuth because the legacy token values no longer take effect after upgrade.
- External RDS installations must create the fixed `bkn_trace` database before upgrade and configure the `dsn` key in Secret `bkn-trace-core-mariadb` to target it. The installation prerequisite rejects missing, invalid, or differently targeted DSNs.
- Internal MariaDB creates the `openbkn`, `safe`, and `bkn_trace` databases with `utf8mb4/utf8mb4_unicode_ci`. Existing databases are not recreated or converted.

## [0.2.2] - 2026-04-10

### Improvements

- Changed the default `agent-observability` chart image tag to the `__VERSION__` placeholder so the packaged chart can inherit the resolved release version during the release workflow.
- Updated the `agent-observability` release workflow to replace the `__VERSION__` placeholder in chart values before packaging, keeping the chart default image tag aligned with the released image tag.

## [0.2.1] - 2026-04-07

### Improvements

- Standardized the `agent-observability` and `otelcol-contrib` Helm chart image values to use `image.registry`, `image.repository`, and `image.tag`, so offline packaging can extract images from the default values.
- Updated the `agent-observability` release workflow to write the resolved release version into the chart default `image.tag` before packaging.

### Upgrade Notes

- If you override `image.repository` with a full image reference that includes the registry, split it into `image.registry` and `image.repository` to match the new chart values structure.

## [0.2.0] - 2026-03-31

### Improvements

- Updated the release workflows under `.github/workflows/` to use the relocated `bkn-trace/` paths for version resolution, Go module discovery, Docker build context, and Helm chart packaging.

### Documentation

- Added a reference command in `README.md` for creating and verifying the multi-architecture manifest of `opentelemetry-collector-contrib` in SWR.

## [0.1.1] - 2026-03-27

### Improvements

- Increased the default result size limit to `1000` for the conversation-based trace search API so a single request can return more matching traces.

## [0.1.0] - 2026-03-25

Initial project release.

### Added

- Added the `agent-observability` service for querying agent traces from OpenSearch.
- Added trace query APIs for raw DSL search and conversation-based lookup, with generated Swagger documentation.
- Added Docker, Helm, and GitHub Actions workflows for building and releasing `agent-observability`.
- Added the `otelcol-contribute-chart` Helm chart for deploying OpenTelemetry Collector Contrib on Kubernetes.
- Added OTLP ingestion and OpenSearch export defaults in the collector chart to support trace and log pipelines.
- Added repository-level English and Chinese README documents describing the Tracing AI architecture, capabilities, and quick start flow.

### Documentation

- Added product and implementation documents for the Agent tracing system under `agent-observability/docs/`, including PRD, design, API schema, and Swagger assets.
