# 015 to 020 Historical Log and Trace Migration

This administrator tool migrates the historical records stored by 015 into the
020 OpenSearch stores used by the product. It is a data upgrade, not an online
legacy reader and not a new event admission path.

- Every source Audit row is written to the 020 SS4O log index.
- Every source Trace Evidence row is written to the 020 evidence index.
- Source timestamps, identifiers, payloads, actor and target values are kept.
- Fields that did not exist in 015 remain absent; the tool does not invent them.
- Stable historical IDs make the operation safe to repeat. Existing historical
  documents are checked and updated only when the converted source document has
  changed.
- The source snapshot is retained so the operation can be rerun and audited.

## Administrator execution

Run after the 020 deployment is healthy, with writes paused according to the
upgrade runbook and the 015 database backup available:

```sh
python3 deploy/scripts/upgrades/0.2.0/historical_trace_data/run_upgrade.py
```

The script discovers the running deployment and reads the retained 015 source
snapshot. It obtains the deployed OpenSearch endpoint, index names and, when
configured, credentials from the deployment Secret. For an in-cluster endpoint
it opens a short-lived port-forward using the service, namespace and port from
that endpoint. Startup is bounded by a timeout, and connection output goes to a
private temporary file so large migrations cannot block on a full pipe. It does
not delete source tables, restore over the running database, or import completed
archive files.

### Customer HTTPS deployments

External HTTPS OpenSearch endpoints use normal certificate verification, with
system trust by default. For a customer/private CA, set the CA bundle on the
upgrade host before running the same command:

```sh
export BKN_HISTORY_OPENSEARCH_CA_FILE=/path/to/customer-ca.pem
```

For an explicitly approved deployment with a self-signed certificate and no CA
bundle, `BKN_HISTORY_OPENSEARCH_TLS_VERIFY=false` disables certificate verification.
This is an operator setting, independent of the endpoint hostname; credentials
still come from the deployment Secret. The default remains verification enabled
for direct endpoints. A script-created Kubernetes port-forward disables
verification by default because its loopback address does not match the service
certificate; operators can override it with the same setting.

The log `deployment.environment` is copied from the target deployment's
`BKN_AUDIT_ENVIRONMENT` configuration. It labels the query environment and does
not rewrite the original event timestamp or business payload.

The administrator path writes directly to OpenSearch and reads the documents
back. It does not require Kafka publication, a native validator binary, or
online event admission for historical rows.

The run writes a private snapshot and report below:

```text
~/.bkn/upgrades/015-to-020-historical/
```

A successful report has `State: completed`, shows the source count equal to
`Source records written to 020 OpenSearch`, and reports zero conflicts. On a
repeat run, documents are reported as already verified or updated; the source
count must still equal the written count.

## Validation

Validate the result using the report and the 020 product/API:

1. Confirm the Audit count in `ss4o_logs-default-namespace` equals the 015 Audit
   source count and that records retain their historical time, readable actor,
   action, target and outcome.
2. Confirm the Evidence count in `bkn-trace-evidence-v2` equals the 015 Evidence
   source count and that trace, span, request, operation and payload fields are
   present where they existed in 015.
3. Set a historical time range in the 020 log search and Trace pages and
   compare representative records with the source snapshot.
4. Run the command a second time. It must complete without conflicts and must
   not create duplicate historical IDs.

For time-range acceptance, also check that the selected current Trace records
retain their question/result previews when the window contains many older
artifacts. Candidate scans and selected-page preview reads have separate budgets.

Trace range lists retain the existing bounded scan and partial/truncated
indicators. The conversion report is the complete migration count; a partial
page is not an exact count of all migrated Trace events.

A Kafka acknowledgement or a database-side audit row alone is not sufficient
for this migration: the corresponding OpenSearch document must be readable by
the 020 query path and visible in the product.

## Development checks

The tool uses only the Python standard library. From this directory run:

```sh
python3 -m unittest discover -s . -p 'test_*.py'
```

For changes to the OpenSearch log reader, run the focused Go tests from
`bkn-trace/agent-observability`:

```sh
GOCACHE=/tmp/openbkn-go-cache go test ./src/drivenadapter/httpaccess/opensearchlogaccess ./src/domain/valueobject/observabilityvo
```
