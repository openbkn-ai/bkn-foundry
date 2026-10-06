# Historical 015 Data Conversion

Development candidate for Foundry #2012. Reads retained source data offline
after upgrading to 020; it does not add a legacy reader to the online service.
Completed archive files/jobs are retained unchanged and are not imported here.

Current support:

- Private immutable SQL-row snapshots, full source fields, hashes and counts.
- Six source-specific management/access Log codecs to native Audit v1.
- Original Evidence Event extraction and native structure/semantic/hash checks.
- Full-source SS4O Span identity conversion and official exporter v0.148.0
  Factory-based nested OTLP expansion, with complete typed source sidecar.
- Explicit `convert`, `archive`, `blocked` classifications and deterministic
  plans. Original HTTP status is required where the target contract requires
  it; success does not imply 200. Deployment `environment` is an operator
  configuration parameter, not a reason to recover the old software environment.
- Qualification-only Audit Kafka publishing and native month/dedup readback.
- Qualification-only Span bulk CREATE with exact readback, frozen mapping,
  numeric/time checks and safe repeated execution.

**Release writes remain disabled.** Qualification writers require approved
full plan, item and target-profile digests and only connect to loopback targets.
Span qualification indexes must begin with `bkn-history-test-`. This candidate
does not claim a full 015-to-020 version-upgrade qualification (G2).

## Build And Test

Go 1.25+, Python 3.11+; Python uses only the standard library. Build the native
validator from `bkn-trace/agent-observability`:

```sh
go build -o /tmp/bkn-historical-data-validate ./cmd/historical-data-validate
go test ./cmd/historical-data-validate ./src/domain/service/ledgersvc
```

From this directory:

```sh
make ci
```

The Span codec uses a temporary loopback HTTP capture server, not a business
OpenSearch endpoint. Running it requires local-listen permission. Its exporter
dataset/namespace are frozen to the 015/020 default `default` / `namespace`;
non-default deployment profiles require a separately frozen codec contract.

## Backup And Plan

Always stabilize the instance and back up retained source tables before an
upgrade/test. Restore SQL backups only to an explicitly isolated disposable
database for repeated testing, never automatically over a running source.

Source profile (no credentials):

```json
{"kind":"kubernetes","context":"kind-bkn-main-e2e","namespace":"resource","pod":"mariadb-0"}
```

An isolated restored source can use `{"kind":"docker","container":"bkn-history-test-2012"}`.
The SQL reader executes read-only consistent-snapshot transactions and uses the
database container's root-password environment, never a password in argv or
logs. It does not execute restore, DROP or source UPDATE commands.

```sh
python3 cli.py snapshot --source-profile source-profile.json --source-deployment INSTANCE --output SOURCE
python3 cli.py plan --source SOURCE --output RUN --environment test --validation-time 2026-10-06T00:00:00Z --native-validator /tmp/bkn-historical-data-validate --span-codec /tmp/bkn-historical-span-codec
python3 cli.py verify --plan RUN
```

Each output directory must be new. Source/plan files are private (0700/0600).
Keep the entire snapshot/plan, not only the summary. Input/provenance and
unconverted fields remain in the source/sidecar. Output counts distinguish
source documents from expanded Span items (`record_count` / `item_count`).
The native validator checks format; it does not prove target durable admission.

## Qualification Writers

Before any write, inspect the private plan and approve all three digests:

- `items_sha256` from `plan.json`.
- SHA-256 of exact `plan.json` file bytes.
- SHA-256 of canonical target-profile JSON (UTF-8, sorted keys, compact,
  non-ASCII preserved). This is not the source Ledger/JCS hash.

Audit profile comes from `apply_audit.audit_profile()`. Set credentials only
in `BKN_HISTORY_KAFKA_USERNAME` / `BKN_HISTORY_KAFKA_PASSWORD`, endpoints in
`BKN_HISTORY_KAFKA_BROKERS`, and mechanism in `BKN_HISTORY_KAFKA_MECHANISM`.
All seed and advertised broker addresses must remain loopback. Native topic
must be `openbkn.audit.v1` with `LogAppendTime` and valid authenticated access.

```sh
python3 cli.py apply-audit --mode qualification --plan RUN --sources vega --expected-items-sha256 ITEMS --expected-plan-sha256 PLAN --expected-profile-sha256 PROFILE --native-validator /tmp/bkn-historical-data-validate --receipt audit-receipt.json
python3 cli.py reconcile-audit --plan RUN --sources vega --target-profile isolated-db.json --output audit-readback.json
```

Kafka ACKs are explicitly not database proof. `reconcile-audit` reads the native
dedup and correct month table and checks payload, content hash, source, event
time and coordinates. Native API/UI and permissions need separate qualification.
Selected blocked sources prevent writing. Unselected sources are reported,
not silently declared migrated. Native consumer dedup handles approved repeat
events; do not automatically retry an uncertain outcome before readback.

Span profile example:

```json
{"endpoint":"http://127.0.0.1:59200","index":"bkn-history-test-2012-span-v1"}
```

Optional `user_env` / `password_env` name environment variables, not values.
No URL credentials are accepted. The writer creates its explicit test index
only after preflight; it never changes an arbitrary existing mapping or deletes
documents. Existing content conflicts fail; identical 409s require readback.

```sh
python3 cli.py apply-spans --mode qualification --plan RUN --expected-items-sha256 ITEMS --expected-plan-sha256 PLAN --expected-profile-sha256 PROFILE --target-profile span-profile.json --receipt span-receipt.json
```

Receipts must have fresh paths, preserve uncertain/partial outcomes, and do not
grant production migration permission. Repeating an approved Span plan with a
new receipt should create zero new documents and verify all existing content.

## Evidence And Core Facts

Extracted Evidence retains its original ID/hash/epoch/sequence; no owner,
sequence or business lifecycle is invented. Wrapper owner stays in the sidecar.
**Do not submit the extracted Event through the Audit writer.** Durable Evidence
migration uses the existing `../evidence_outbox_to_kafka` controlled manifest
bridge and `evidence-migration-admin`; it requires trusted old Core dependencies,
ownership, target-watermark/causality checks and activation. Format success does
not satisfy these gates. Outbox runtime cursors compare numeric primary keys.

Valid old Core facts are preserved with their migration ledger and upgraded by
the normal target schema migration; they are not reconstructed from Outbox
events. Missing old facts cannot be replaced with current-directory lookups or
new synthetic interactions. The 8081 sample has old producer rows but no matching
old central facts, so it cannot prove their full durable replay or UI visibility.
