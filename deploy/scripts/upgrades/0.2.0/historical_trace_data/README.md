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

The script prints the Markdown report path. Exit 0 means every source record was
converted and read back in native targets. Field defaults and ID remappings are
reported separately; they do not remove whole records. Exit 2 means the upgrade
is incomplete. Inspect the report, resolve the failure, and repeat the command.

## Sources and native targets

| Input | Native 020 target | Conversion |
| --- | --- | --- |
| Backend, Vega, Execution Factory, Model Manager and Safe management/access tables | Native Audit ledger used by log search and its audit filter | Map the stored row to an Audit event; publish through the existing writer and confirm its native monthly row, dedup identity, payload and hash. |
| Retained center Conversation, Interaction, Operation, Receipt and related facts | Original native tables and Core projection alias | Keep the facts. Check the deployed schema and verify authoritative projection bodies, versions and counts. Reuse the native rebuild service only if the existing projection does not match. |
| Backend/ontology Evidence outboxes | Native Evidence aggregate index | Convert stored events and their relationships into native Conversation, Interaction, Operation, Receipt and call-fact rows; rebuild Core projections and write native Evidence aggregates. Preserve event IDs, times, payloads and references. Split reused Trace IDs by request/context so existing native lookups can find every request. |
| Existing native SS4O technical Span index | Same native SS4O storage | Preserved by the platform upgrade; this SQL snapshot does not export or reimport that index. Evidence events are never converted into invented technical Spans. |
| Existing Artifact content and references | Existing native Artifact storage | Preserved; this tool does not restore archive files or create missing content. |

Audit is a category of the shared log data. Management events are `audit.admin`;
login/logout events are `access.user`. The audit workbench filters the same
native records. A raw SS4O log copy is not a second audit migration target.

## Field conversion and conflicts

- Missing HTTP status: success defaults to 200; stored HTTP status text maps to
  its numeric status (for example Service Unavailable to 503, Bad Request to
  400). Other failures default to 500 and denied results to 403. The stored
  outcome remains unchanged.
- Labels beyond native limits are truncated with an ellipsis; oversized object
  identifiers become stable type/hash identifiers. The full original remains
  in the private source snapshot and per-record residue.
- Keep source actor and object labels. If a label is unavailable, retain its
  source identifier; do not add directory lookups or UI fallback logic.
- Split a reused Trace ID by request/conversation/owner context using a stable
  hash. Update receipts and Evidence together. Remap reused or oversized Core
  IDs deterministically to fit native constraints. Keep source-to-target IDs
  in the private per-record report.
- Generate the native Core records needed to represent stored observations.
  Missing terminal state defaults to completed observation; observed failure
  stays failed. Start/end times use captured bounds. Missing auth method is
  unknown; missing protocol defaults to MCP. Agent name retains the source
  application principal. Missing full question/answer remains empty; captured
  observation payload is available in call details.
- Evidence aggregates retain all original observations and include the normal
  `retrieval.completed` projection of converted receipts. Explicit Trace-ID
  queries therefore read the same terminal state and tool name as Core-backed
  lists. These derived projection events do not increase source-row counts.
- Use native transactions and native record-integrity calculation. Exact target
  matches are skipped. Conflicting existing Core records are never overwritten.
  Existing aggregates may be replaced only when they exactly match the frozen
  source-derived pre-conversion aggregate or its exact mapped observation-only
  representation, using sequence/primary-term checks.
- A failed conversion, write, or readback leaves the upgrade incomplete. The
  original record stays available for a corrected rerun.

`final-items.jsonl` records each source row, target identity, field defaults and
ID mapping. Source snapshots remain unchanged. Core projection counts include
existing and converted Core rows and are separate from source-record counts.

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
3. Check converted Trace list entries, request Evidence chains and business
   references through ordinary native queries. Every converted request must be
   reachable using its mapped identity.
4. Select the original historical time range in the product. Field defaults and unavailable
   question/answer content are listed in the report; no UI repair is needed.
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
