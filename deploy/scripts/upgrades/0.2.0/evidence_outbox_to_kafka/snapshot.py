"""Read-only, timestamp-bounded snapshots of the two historical Event tables."""

from manifest import ManifestError, entries_digest
from source import classify_row

_EVENT_TABLES = (
    "bkn_backend_trace_outbox",
    "ontology_query_trace_outbox",
)


def read_event_snapshot(connection, source_snapshot_at):
    """Return rows ordered by the frozen source-table/primary-key cursor.

    `connection` is a DB-API read-only source connection supplied by the
    maintenance job. This adapter deliberately has no central-DB handle and
    performs no source mutation. The caller owns transaction isolation and
    supplies one already frozen timestamp shared by both source services.
    """
    if not isinstance(source_snapshot_at, str) or not source_snapshot_at:
        raise ManifestError("source snapshot timestamp is required")
    rows = []
    for table in _EVENT_TABLES:
        cursor = connection.cursor()
        try:
            cursor.execute(
                "SELECT outbox_id,event_id,payload_hash,producer_id,producer_stream_id,"
                "producer_epoch,producer_sequence,envelope,status,locked_until "
                f"FROM {table} WHERE updated_at <= %s ORDER BY outbox_id ASC",
                (source_snapshot_at,),
            )
            columns = [column[0] for column in cursor.description]
            for values in cursor.fetchall():
                row = dict(zip(columns, values, strict=True))
                row["source_table"] = table
                rows.append(row)
        finally:
            cursor.close()
    return sorted(rows, key=lambda row: (row["source_table"], row["outbox_id"]))


def verify_frozen_entries(rows, manifest, entries):
    """Re-classify a bridge source reread against a payload-free artifact.

    The artifact contains only immutable C1 entries. Event values remain in
    the source database and are returned in memory solely for the immediate
    Kafka send; they are never written into the artifact or passed to the
    center-only admin/reconciler commands.
    """
    if not isinstance(manifest, dict) or manifest.get("entry_count") != str(len(entries)):
        raise ManifestError("frozen artifact count does not match manifest")
    actual_entries, events = [], {}
    for row in rows:
        entry, event = classify_row(row, manifest.get("manifest_id"), manifest.get("source_snapshot_at"))
        actual_entries.append(entry)
        if event is not None:
            events[(entry["source_table"], entry["source_primary_key"])] = event
    if len(actual_entries) != len(entries) or entries_digest(actual_entries) != manifest.get("entries_digest"):
        raise ManifestError("source reread does not match frozen migration entries")
    expected = sorted(entries, key=lambda entry: (entry["source_table"], entry["source_primary_key"]))
    actual = sorted(actual_entries, key=lambda entry: (entry["source_table"], entry["source_primary_key"]))
    if any(
        {key: candidate[key] for key in candidate if key != "entry_id"}
        != {key: frozen[key] for key in frozen if key != "entry_id"}
        for candidate, frozen in zip(actual, expected, strict=True)
    ):
        raise ManifestError("source reread entry differs from frozen artifact")
    return events
