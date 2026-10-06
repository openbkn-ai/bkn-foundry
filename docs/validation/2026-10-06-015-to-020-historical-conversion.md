# 015 to 020 Historical Conversion Report

This report records the repeatable validation of the administrator upgrade
entry point for Foundry #2012 on the local 020 instance (`kind-bkn-main-e2e`).

## Run result

The run was executed with:

```sh
python3 deploy/scripts/upgrades/0.2.0/historical_trace_data/run_upgrade.py
```

The report was written to the fixed private upgrade directory:
`~/.bkn/upgrades/015-to-020-historical/run-20261006T122301-33555993/report.md`.

| Measure | Result |
| --- | ---: |
| Source records scanned | 3734 |
| Native 020 Audit records verified | 1 |
| OpenSearch log documents created | 0 |
| OpenSearch log documents already verified | 1 |
| OpenSearch log conflicts | 0 |
| Records retained with a reason | 3733 |

The run completed with retained records. The retained reasons were:

| Reason | Count |
| --- | ---: |
| `ambiguous_snapshot` | 57 |
| `evidence_dependency_set_not_verified` | 3628 |
| `missing_http_status` | 46 |
| `unverified_actor_origin` | 2 |

The one verified Audit event is:

- event ID `e162af73-7f8e-5d15-b4fe-6ca14c9a57e4`
- source `vega`
- event `vega.operation.observed`
- historical timestamp `2026-09-12T21:25:44.168466Z`
- outcome `failure`
- target `worldcup_mysql_vega`

It is stored in MariaDB's native `bkn_audit` ledger through the existing
Kafka/consumer path and is also present in the configured OpenSearch log index
`ss4o_logs-default-namespace`. The OpenSearch document uses the stable ID
`historical-audit:vega:e162af73-7f8e-5d15-b4fe-6ca14c9a57e4`, preserves the
historical timestamp and target snapshot, and has no fabricated `traceId` or
`spanId`. A direct OpenSearch readback using the normal log filters returned the
same document. Repeating the migration produced `already verified = 1` and no
new document.

## Operational boundary

The script publishes only Audit events that first pass native 020 validation
and MariaDB readback. When the deployment advertises an in-cluster OpenSearch
endpoint, the host-side entry point creates a short-lived `kubectl
port-forward` to the existing OpenSearch service and closes it after the
idempotent create/readback operation. No new service, index, or legacy reader
is introduced.

Evidence rows without their historical Conversation, Interaction, Operation,
owner, causality and watermark remain in the private source/plan material.
They are not converted into logs or synthetic Trace facts. No authentic 015
technical Span source was found in the deployed indexes, so no Span is
fabricated from a current 020 projection.

The upgrade tool and its administrator guide are in
`deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md`.
