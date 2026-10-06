# 015 to 020 Historical Conversion Validation

This report records the repeatable validation of Foundry #2012 on the local
020 instance (`kind-bkn-main-e2e`).

## Latest run

Command:

```sh
python3 deploy/scripts/upgrades/0.2.0/historical_trace_data/run_upgrade.py
```

Report:
`~/.bkn/upgrades/015-to-020-historical/run-20261006T141446-dadbab67/report.md`

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
| Evidence documents updated | 2648 |
| Evidence documents already verified | 980 |
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
