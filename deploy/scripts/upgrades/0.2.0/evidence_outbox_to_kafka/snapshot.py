"""Read-only, timestamp-bounded snapshots of the two historical Event tables."""

from manifest import ManifestError

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
