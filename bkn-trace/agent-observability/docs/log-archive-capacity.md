# Log archive capacity and recovery (#2947)

The unit is an OpenSearch log document, not a line of text, Interaction or Span.
Log retention stays 30 days. Trace retention, candidate budgets, SQL deletion
scope and projection lifecycle are unchanged. The HTTP contract is documented in
[log-archive.openapi.yaml](api/log-archive.openapi.yaml).

## Owner configuration

| Setting | Default | Valid range | Meaning |
| --- | ---: | ---: | --- |
| `BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_CANDIDATES` | 10000 | 1..10000 | Maximum documents per selection |
| `BKN_OBSERVABILITY_LOG_ARCHIVE_MAX_PAYLOAD_BYTES` | 10485760 | 1024..67108864 | JSONL bytes, including newlines, per batch |
| `BKN_OBSERVABILITY_LOG_ARCHIVE_LIMITS_FILE` | unset | JSON file <=4 KiB | Optional per-batch override |

The file accepts only `max_candidates` and `max_payload_bytes`; omitted fields
inherit environment/default values. For example:

```json
{"max_candidates":10000,"max_payload_bytes":10485760}
```

The deployment owner controls these settings, with existing deployment/ConfigMap
permissions. Archive callers cannot change them through the empty POST body.
Helm exposes them under `observability.archive.logLimits`. Set
`existingConfigMap` to a ConfigMap with a `limits.json` key for live updates. Its
directory is mounted read-only, without `subPath`; Kubernetes projection delay
still applies. The application reads a fresh, validated snapshot at each batch
boundary. Invalid, unreadable, unknown-field or out-of-range configuration stops
the next batch with 503, without changing an already-frozen batch or rolling back
completed prefixes. Roll back by restoring the previous valid file/configuration.
Environment/Helm changes without a mounted override require a normal rollout.

## Effective database and read bounds

Preparation uses the minimum of the actual connection's SESSION packet limit,
the current GLOBAL packet limit, and the configured driver's `maxAllowedPacket`
when nonzero. It reads limits and inserts the frozen checkpoint on the **same
connection**. The configured JSONL limit is an additional independent constraint.
It reserves 64 KiB for statement framing, job/progress/manifest metadata and uses
twice the exact candidate/ID JSON size as a conservative SQL escaping bound.
Candidate JSON includes Base64 payloads and frozen document metadata. Thus a
16 MiB server may produce fewer than 10000 documents per batch; the count is
computed from actual content, not fixed at 5000. This bound intentionally favors
headroom over filling packets to their exact wire maximum.

The production OpenSearch client retains its 16 MiB response ceiling. Selection
size is reduced when a response exceeds that bound; a single oversized record
gets a precise identity/stage and a measured lower bound (`at_least=true`), rather
than being truncated or silently skipped. Configuring a larger JSONL limit does
not remove database, client or per-response constraints.

Only the database owner should change server capacity. `SET GLOBAL
max_allowed_packet=<bytes>` affects new sessions; existing SESSION values remain
unchanged. Persist the corresponding `[mariadb] max_allowed_packet=<value>` in the
owner-managed server option file/Helm configuration for restarts. An increase may
also require existing connection pools to recycle and the client's ceiling to be
raised. A decrease is honored by preparation's GLOBAL minimum, but first drain
or assess pending large checkpoints: lowering the server below an old stored
recovery row can prevent reading it until capacity is restored. The application
never executes SET GLOBAL, replaces server configuration or rewrites old frozen
batches to force them under a new limit.

The Go driver defaults its client ceiling to 64 MiB. `maxAllowedPacket=0` queries
the server at each new connection. Interpolation on/off is supported by the
conservative bound. These relationships are covered with a disposable MariaDB
instance, including unchanged old sessions, changed new sessions and a smaller
client ceiling. See the [driver documentation](https://github.com/go-sql-driver/mysql#maxallowedpacket)
and [MariaDB variables](https://mariadb.com/docs/server/server-management/variables-and-modes/server-system-variables#max_allowed_packet).

## Workflow and retained recovery content

1. Freeze eligible log IDs, raw source, timestamp, concrete index, sequence,
   primary term and routing. Validate completeness before accepting candidates.
2. Fit a prefix using the limits snapshot and effective checkpoint budget; save
   its **complete** candidates before upload. Its existing status is `failed`,
   with `checkpoint_prepared=true`, `stage=prepared`, no manifest and no verified
   count. Preparation alone never permits deletion.
3. Upload data and manifest, then read back at most the expected length plus one
   and verify SHA-256. Failure stages distinguish data upload, manifest upload
   and readback verification. Signed URLs/upstream errors are not exposed.
4. Commit verified manifest/state with a small metadata-only update. Complete
   candidate content is not rewritten after upload. Only this durable transition
   permits cleanup. If it fails, source remains intact and frozen recovery input
   stays available for inspection.
5. Delete only frozen document versions. A concurrent update yields incomplete
   cleanup and keeps the newer document. Confirm all frozen IDs absent before
   marking complete. A failed final state write remains resumable; it does not
   advance cleaned counts twice or cause a second upload.
6. Mark the batch complete and clear its duplicate checkpoint payload. The next
   batch reads a fresh limits snapshot while keeping the original run cutoff.

Legacy `candidate_payloads` arrays remain supported. For legacy logs without
version metadata, bounded grouped reads must match archived content before
freezing a current version for conditional deletion. Changed content or an
unverifiable response stops cleanup for manual review. Existing Trace recovery
and its source fences remain unchanged.

## Why retain the full database checkpoint

The original fields supply trusted source identities, expected archive bytes and
recovery input independently of object storage. JSONL `_source` does not reliably
contain the OpenSearch ID, routing or frozen version. Keeping only an object URL
would lose these guarantees; replacing a file and its manifest could otherwise
substitute cleanup authority.

This fix retains those fields and the existing archive format, with no migration.
The tradeoff is duplicate content until completion, now bounded and written
before upload. Moving content out of SQL remains a separate protocol decision:
it needs a database-anchored digest/length/object version, explicit ID-to-record
mapping, retention/overwrite rules, bounded reconstruction and legacy recovery.
No such field removal or storage migration is hidden in this repair.

## Failure inspection and orphan handling

Errors include request ID, stage and recorded archive job ID when available.
Capacity/configuration failures require operator action rather than an automatic
retry of unchanged input. Per-batch verified/cleaned/remaining counts describe
selection-time progress, never an all-or-nothing cross-storage transaction.

A process can die between upload and the metadata commit, or before a failed-state
update succeeds. Locate its objects by the deterministic
`<archive-prefix>/<archive_job_id>/{data.jsonl,manifest.json}` and compare against
that job's frozen checkpoint. A file's existence is not completion. Do not enable
cleanup merely by setting a manifest URL or deleting recovery fields. Existing
`retry-cleanup` only accepts verified cleanup-incomplete jobs and revalidates the
archive. Failed/unverified jobs require read-only inspection or a new normal run;
source stays hot and historical orphan objects are not automatically deleted.
Any manual data restoration, deletion or promotion of an old failed job needs a
separate reviewed operation.
