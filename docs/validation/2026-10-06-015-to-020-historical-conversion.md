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
| Source records written to 020 OpenSearch | 3734 |
| OpenSearch log documents created | 0 |
| OpenSearch log documents already verified | 106 |
| OpenSearch log conflicts | 0 |
| OpenSearch evidence documents created | 0 |
| OpenSearch evidence documents already verified | 3628 |
| OpenSearch evidence conflicts | 0 |
| Source records not written | 0 |

The run completed with retained records. The retained reasons were:

| Reason | Count |
| --- | ---: |
| `ambiguous_snapshot` | 57 |
| `evidence_dependency_set_not_verified` | 3628 |
| `missing_http_status` | 46 |
| `unverified_actor_origin` | 2 |

The one native Audit event that also passed the 020 Audit ledger validator is:

- event ID `e162af73-7f8e-5d15-b4fe-6ca14c9a57e4`
- source `vega`
- event `vega.operation.observed`
- historical timestamp `2026-09-12T21:25:44.168466Z`
- outcome `failure`
- target `worldcup_mysql_vega`

It is stored in MariaDB's native `bkn_audit` ledger through the existing
Kafka/consumer path and is also present in the configured OpenSearch log index
`ss4o_logs-default-namespace`. All 106 015 Audit rows are present in that
index; the OpenSearch document for this event uses the stable ID
`historical-audit:vega:e162af73-7f8e-5d15-b4fe-6ca14c9a57e4`, preserves the
historical timestamp and target snapshot, and has no fabricated `traceId` or
`spanId`. A direct OpenSearch readback using the normal log filters returned the
same document. Repeating the migration produced `already verified = 1` and no
new document.

## Operational boundary

The script writes every source Audit and Evidence row to its corresponding
020 OpenSearch index. When the deployment advertises an in-cluster OpenSearch
endpoint, the host-side entry point creates a short-lived `kubectl
port-forward` to the existing OpenSearch service and closes it after the
idempotent create/readback operation. No new service, index, or legacy reader
is introduced.

015-only fields remain in the source snapshot when 020 has no equivalent field;
they do not prevent migration. The 3628 evidence documents are visible in the
020 evidence index with their original trace, span, request, operation and
payload fields.

The upgrade tool and its administrator guide are in
`deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md`.
