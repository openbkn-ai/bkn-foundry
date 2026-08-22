# BKN Trace

[中文](README.zh.md)

[![License](https://img.shields.io/badge/license-OpenBKN-blue.svg)](../LICENSE-OPENBKN.txt)

BKN Trace is the OpenBKN Community Trace Core. It records first-hand facts
about managed Agent, SDK, and MCP execution, then provides protected APIs for
technical trace analysis and precise correlation with platform logs.

## What it records

The authoritative unit of execution is an Operation attempt. A completed or
failed attempt can retain its Conversation, Interaction, Operation and request
identifiers, execution timing and status, caller identity, protocol and tool
information, and the input, output, or diagnostic error received at the call
boundary. Recording must never alter the business tool's original result.

BKN Trace records facts rather than inferring them from a final answer. Missing
information remains explicitly missing; it is not reconstructed by a reader or
UI.

## Public Community capabilities

- Accept and persist managed execution facts and associated evidence events.
- Provide protected Trace, Conversation, Interaction, Operation, evidence, and
  technical-log read APIs.
- Return technical execution detail: recorded input/output or errors, Trace and
  Span relationships, and exact correlation identifiers for log drill-down.
- Enforce access profiles and record scope before returning trace or evidence
  data.
- Retain the Community Trace fact model and public API contracts as stable
  integration boundaries for OpenBKN clients.

## Architecture

```text
Managed MCP / SDK calls
        |
        v
Trace producers capture first-hand execution facts
        |
        v
BKN Trace Core
  - lifecycle and operation facts
  - evidence and technical correlations
  - access-controlled read APIs
        |
        +--> technical Trace analysis
        +--> precise log drill-down
```

The core is deliberately a technical fact service. It does not derive business
meaning from a final response or add a second authoritative trace store.

## Components

| Path | Responsibility |
| --- | --- |
| `agent-observability/` | Go Trace Core service, OpenAPI surface, persistence adapters, and deployment chart. |
| `otelcol-contribute-chart/` | OpenTelemetry Collector Contrib chart for OTLP collection and OpenSearch export. |
| `scripts/` | Repeatable contract and deployment-safety checks. |

See [agent-observability/README.md](agent-observability/README.md) for local
development and service configuration, and
[otelcol-contribute-chart/README.md](otelcol-contribute-chart/README.md) for
Collector deployment and validation.

## Verification

Run the BKN Trace license-header check from the Foundry repository root:

```bash
python3 bkn-trace/agent-observability/scripts/check_license_headers.py
```

Run service tests from the service directory:

```bash
GOCACHE=/tmp/openbkn-go-build-cache GOMODCACHE=/tmp/openbkn-go-mod-cache go test ./...
```

## License

BKN Trace is licensed under the [OpenBKN License](../LICENSE-OPENBKN.txt).
Each OpenBKN-authored, commentable source and deployment file carries a
copyright notice and `SPDX-License-Identifier: LicenseRef-OpenBKN` marker.
