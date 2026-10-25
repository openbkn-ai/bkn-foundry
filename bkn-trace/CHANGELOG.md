# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]

### Changed

- Audit source metadata scopes availability to Ledger queries and reports Safe publisher observations separately. An optional, bounded background probe reads one Safe instance's aggregate status; missing or stale observations remain unknown. Delivery recovery preserves durable historical-loss evidence. Publisher/history diagnostics remain independent of query partial flags and stored-record count precision. The log UI consumes this evidence without additional query-time probes. No schema migration is added. (#2109)

- Record-integrity materialization now persists retry diagnostics per interaction/source generation, with at most 8 attempts in 30 minutes and 30-second to 5-minute backoff. Authorized readers can inspect the current generation through read-only `GET /api/agent-observability/v1/interactions/{interaction_id}/record-integrity-status`; reads neither inspect evidence live nor requeue work, and source changes schedule a new generation while preserving prior retry history. Migration 039 requires separate approval before deployment. Older images reject unknown migration ledger versions: safe rollback requires retaining a compatible migration manifest and separately reviewed worker shutdown, rather than directly rolling back the image. No manual requeue or UI is added. (#2149)

- Historical capture can use bounded gzip-body JSON when base64 expansion exceeds the package budget, preserving raw bytes, identity, gaps, old v1 reads and small-package hashes. Encoded and aggregate decoded budgets remain enforced; Revision inputs stay v1. (#2148)

- Trace scanner Tool matching uses the authorized receipt snapshot after archive purge removes SQL receipts. PIT cleanup failures no longer discard complete successful results; deterministic scan-limit errors return 422 while deadlines return 503. (#2144)

- Tool-only and Service+Tool Trace filtering use scoped canonical identity pagination instead of the global 2,000-candidate scan. Root tool metadata remains consistent across filter toggles and bounded projections, sharing the existing root-service metadata roundtrip. Migration 037 adds a nonunique receipt lookup index; its rollback still requires a compatible migration manifest. (#2144)

- Core Trace lists add an optional bounded PIT scanner for Status/ErrorKeyword, their Service/Tool combinations, and shared business-network scopes without unsupported content filters. Complete Interaction authorization and authorized-tuple SQL reads protect root-service/error metadata; keywords match the final 512-code-point summary. Deterministic resource refusals return HTTP 422 `SUMMARY_QUERY_BUDGET_EXCEEDED`; query deadlines return HTTP 503 `SUMMARY_QUERY_TIMEOUT`; non-global scanned lists keep span statistics unavailable because the old statistics port is unscoped. No additional migration or materialized read model is introduced; ordinary list/detail fast paths remain unchanged. See the README for budgets, PIT permissions and snapshot limits; unsupported filter combinations retain their potentially partial compatibility behavior. (#2144)

- One log-archive create request processes successive verified batches of up to 10,000 records, with an independent job/manifest and persisted progress for each batch. Cleanup deletes only frozen IDs and confirms their absence before continuing; retry resumes verified cleanup without rewriting the object. Same-kind operations are serialized. Untrusted candidate responses still return 409, but a large eligible population alone no longer blocks archiving. Migration 038 adds nullable batch progress; compatible-manifest rollback is required. This is count batching, not complete streaming/OOM remediation. (#1724)

- Service-filtered Trace lists compute the total and nonempty page in one SQL statement, reducing repeated canonical service lookups while preserving cursor totals and empty-page behavior. Added isolated MariaDB/OpenSearch capacity coverage. (#2150)

- Interaction operation reads support a payload-free `view=summary` while retaining the default full response. Service-only Trace filtering now uses canonical MariaDB identity count/page queries instead of a fixed projection candidate scan. (#2005, #2143, #2145)

- Record-integrity materialization failures now use process-local exponential backoff from 30 seconds to 5 minutes, reducing repeated evidence reads and error logs. Source changes bypass backoff; dependency recovery remains retryable without changing integrity verdicts. (#2133)

- Trace/Evidence admission-budget failures now report configuration fields or metric sources through existing error details and Debug diagnostics without repeated polling WARNs. `BKN_TRACE_LOG_LEVEL` (Helm `logLevel`, default `info`) enables these diagnostics at runtime startup. Collector queue sampling rejects missing size and invalid values instead of treating missing data as a healthy zero, while source coverage continues reading refused/failed counters.

- Disabled Audit Kafka consumers remain visible as an unconfigured `audit-ledger`. Audit queries return an actionable, non-retryable configuration error; mixed queries disclose the audit gap. The installer preserves consumer Helm values and checks existing credential and MariaDB Secret references before applying the release.
- Trace Core now owns creation of `bkn_trace_ee_provenance_analyses` in the versioned MariaDB migration manifest, so Community and Enterprise images initialize the same schema before optional Enterprise routes use it.
- Renamed the module directory from `trace-ai/` to `bkn-trace/` to align with the platform-wide `bkn-*` naming (display name: BKN Trace). The Go module path changed to `github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability`; CI/release workflows, CODEOWNERS, and issue routing were updated accordingly. Image and chart names (`agent-observability`, `otelcol-contrib`) are unchanged.
- Complete OpenBKN installations persist Trace Core in the fixed `bkn_trace` MariaDB database and Evidence in OpenSearch. Offline installations mirror the Evidence index hook image into the configured offline registry; online installations keep the chart registry unless explicitly overridden.
- Managed lifecycle writes remain available through the cluster-internal `8081` Service and are now also exposed on public port `8080` for OAuth-authenticated SDK clients. Public writes derive owner identity server-side and are restricted to the deployment-approved tenant. Evidence Event/Artifact writes remain on public port `8080` and use the independent ingest token.
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
