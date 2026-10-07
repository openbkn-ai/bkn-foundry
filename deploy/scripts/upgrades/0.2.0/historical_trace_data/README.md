# 015 to 020 Historical Data Upgrade

This is a one-time **data and projection conversion**. It adds no historical
query branch, legacy reader, UI fallback or live producer behavior. Stored 015
records are the input. Lossy conversion is allowed and reported.

## Run the upgrade

Back up the 015 databases and persistent stores, pause business writes, deploy
020, and wait for its database schema migrations and services to become healthy.
Use the release containing this tool and its `historical-data-validate` command.
Run on the upgrade host with Python 3 and kubectl access to the existing cluster:

```sh
python3 deploy/scripts/upgrades/0.2.0/historical_trace_data/run_upgrade.py
```

The tool discovers the current Kubernetes deployment, its database pod and
observability configuration. It freezes the retained source rows on the first
run and reuses that same snapshot on repeat runs. Cluster UID identifies the
instance; renaming a kubectl context does not change the source identity.

No source/target credentials need to be passed on the command line. Secrets
remain in the existing deployment. Reports and source snapshots are private:

```text
~/.bkn/upgrades/015-to-020-historical/instance-<cluster-digest>/source/
~/.bkn/upgrades/015-to-020-historical/run-<timestamp>-<id>/report.md
```

The script prints the Markdown report path. Exit 0 means the input was accounted
for, including reported losses. `completed_with_loss` does **not** mean every
source row or every product view was converted. Exit 2 means execution failed or
some writes still need reconciliation; inspect the report before retrying.

## Sources and native targets

| Input | Native 020 target | Conversion |
| --- | --- | --- |
| Backend, Vega, Execution Factory, Model Manager and Safe management/access tables | Native Audit ledger used by log search and its audit filter | Map the stored row to an Audit event; publish through the existing writer and confirm its native monthly row, dedup identity, payload and hash. |
| Retained center Conversation, Interaction, Operation, Receipt and related facts | Original native tables and Core projection alias | Keep the facts. Check the deployed schema and verify authoritative projection bodies, versions and counts. Reuse the native rebuild service only if the existing projection does not match. |
| Backend/ontology Evidence outboxes | Native Evidence aggregate index | Extract the stored inner event and owner; combine representable events by Trace into the native `aggregate-<hash>` document, preserving event IDs, times, payloads and references. Read back each aggregate. |
| Existing native SS4O technical Span index | Same native SS4O storage | Preserved by the platform upgrade; this SQL snapshot does not export or reimport that index. Evidence events are never converted into invented technical Spans. |
| Existing Artifact content and references | Existing native Artifact storage | Preserved; this tool does not restore archive files or create missing content. |

Audit is a category of the shared log data. Management events are `audit.admin`;
login/logout events are `access.user`. The audit workbench filters the same
native records. A raw SS4O log copy is not a second audit migration target.

## Losses and conflicts

- Missing native required fields, such as HTTP status, leave that source row
  unconverted; the report names the reason. No success status is substituted.
- Stored actor/target names are retained even when a name equals an ID. The tool
  does not require historical identity proof or consult today's directory to
  change old names. Identifier-only source labels remain identifier-only.
- A native Evidence aggregate has one request/context/owner. Incompatible source
  contexts for the same Trace are retained with a specific reason; the tool does
  not invent replacement Trace, Conversation or Operation lifecycles.
- Converted Evidence without a matching Core receipt can be read as native
  Evidence, but cannot become an entry in the ordinary receipt-based Trace list.
  This missing association is recorded as `missing_core_receipt`.
- Existing native aggregate content conflicts are left unchanged. Failed writes
  and unavailable readback are execution failures, not successful losses.

`final-items.jsonl` records each source row's final conversion outcome, target ID
and association losses. The original complete row stays in the source snapshot.
Core projection counts are separate from converted source-row counts.

## Customer HTTPS deployments

Direct HTTPS endpoints verify certificates by default. For a customer CA, set
its bundle on the upgrade host:

```sh
export BKN_HISTORY_OPENSEARCH_CA_FILE=/path/to/customer-ca.pem
```

The Core helper receives this public CA bundle over stdin; it does not require
tar or a shell in the running observability container. The aggregate writer uses
the same host bundle. A deployment that explicitly requires certificate bypass
can set `BKN_HISTORY_OPENSEARCH_TLS_VERIFY=false`; this operator setting applies
to customer endpoints, not just localhost. Direct endpoints remain verified by
default. Script-created Kubernetes forwarding uses the configured service,
namespace and port; verification defaults off for that loopback connection to
avoid a service-certificate hostname mismatch, and can be explicitly overridden.

## Verification and recovery

1. Read the source, converted, retained and loss counts in `report.md`. Core
   projection verification and aggregate document counts are listed separately.
2. Use an unmodified 020 query path to compare converted log IDs and payloads;
   the audit filter must return the same management records once each.
3. Check retained native Trace records and converted Evidence in their ordinary
   queries. Do not count Evidence-only rows as Trace list entries.
4. Select the original historical time range in the product. Missing lifecycle,
   names, previews or content must be listed as losses, not repaired in the UI.
5. Run the command again. Matching Audit identities are not republished, matching
   aggregates are skipped, and an already matching Core projection is not rebuilt.

The source tables, completed archives and original snapshot are not deleted.
The command does not restore a database over existing 020 facts. Keep the upgrade
backup until the customer accepts the result; any full rollback follows the
platform backup/restore procedure, not a per-row delete operation from this tool.

## Development checks

```sh
python3 -m unittest discover -s deploy/scripts/upgrades/0.2.0/historical_trace_data -p 'test_*.py'
```

In `bkn-trace/agent-observability`, run `go test ./...`, `go vet ./...` and
`go build ./cmd/...`. The offline Core helper reuses the native projection source
and rebuild service; product query code must have no migration-specific diff.
