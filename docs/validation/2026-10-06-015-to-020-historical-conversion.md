# 015 to 020 Historical Conversion Validation

This report records the repeatable validation of Foundry #2012 on the local
020 instance (`kind-bkn-main-e2e`).

## Latest run

Command:

```sh
python3 deploy/scripts/upgrades/0.2.0/historical_trace_data/run_upgrade.py
```

Report:
`~/.bkn/upgrades/015-to-020-historical/run-20261006T162225-7b1e0dfc/report.md`

| Measure | Result |
| --- | ---: |
| Source records scanned | 3734 |
| Audit source rows | 106 |
| Trace Evidence source rows | 3628 |
| Source records written to 020 OpenSearch | 3734 |
| Log documents created | 0 |
| Log documents updated | 0 |
| Log documents already verified | 106 |
| Evidence documents created | 0 |
| Evidence documents updated | 0 |
| Evidence documents already verified | 3628 |
| Conflicts | 0 |
| Source records not written | 0 |

The state is `completed`. The run was repeated against the existing 020
indices; existing historical documents were read back, and documents whose
converted representation had changed were updated by stable historical ID.
The second run therefore proves idempotent migration rather than creating
additional rows.

## Data and product checks

All 106 Audit rows were written to `ss4o_logs-default-namespace`, and all 3628
Evidence rows were written to `bkn-trace-evidence-v2`. Source timestamps,
identifiers, payloads, actor/target values and correlation identifiers were
preserved. A field that has no 020 equivalent remains absent; no historical
value was invented.

The native 020 Audit ledger also contains the previously verified event
`e162af73-7f8e-5d15-b4fe-6ca14c9a57e4` from `vega`, with historical timestamp
`2026-09-12T21:25:44.168466Z`, failure outcome and target
`worldcup_mysql_vega`. The OpenSearch copy uses the stable ID
`historical-audit:vega:e162af73-7f8e-5d15-b4fe-6ca14c9a57e4` and preserves the
source event without adding a fabricated trace or span.

The administrator guide and executable entry point are in
`deploy/scripts/upgrades/0.2.0/historical_trace_data/README.md`.

## Review feedback verification

- Kubernetes forwarding resolves the configured service, namespace and remote
  port. Startup notices, large connection output and timeout cleanup are covered.
- Write counts are assigned only after document readback. A failed write or
  mismatched readback counts once as a conflict; short replies stop the run.
- Python migration tests: 113 passed.
- The real rerun read back all 3734 documents without updates or conflicts.

PR approval, merge and merged-image 8081 acceptance remain pending. Native
library verification is not a substitute for the merged-image product check.

## Review blocker 14279111

The ordinary time-range Trace list now scans evidence candidates without
artifacts, then hydrates only the selected page's Trace IDs through the existing
receipt-authorized artifact path. Preview hydration is not restricted by the
candidate time window. Request, Interaction and Conversation summary reads keep
their existing artifact behavior. Time-range evidence candidates are sorted by
observed start descending with document ID as the tie-breaker.

Regression coverage includes 1002 older artifacts, selected-page question/result
previews, mixed historical/current cursor pages, and ordinary summary artifact
retention. Full agent-observability Go tests and build passed. PR re-review and
merge approval remain pending; no merged-image acceptance is claimed here.
