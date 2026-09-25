"""Frozen historical-outbox classification and migration Record encoding."""

import json
from datetime import datetime, timezone

from manifest import ManifestError

_SOURCES = {
    "bkn_backend_trace_outbox": ("bkn-backend", "bkn-backend", "bkn-backend"),
    "ontology_query_trace_outbox": ("ontology-query", "bkn-ontology", "ontology-query"),
}
_PUBLISH = {"pending", "retry"}
_COVERAGE_GAP = {"abandoned", "conflict", "dlq"}
_STRING_IDENTITY = ("event_id", "payload_hash", "producer_id", "producer_stream_id")
_NUMERIC_IDENTITY = ("producer_epoch", "producer_sequence")
_UINT64_MAX = (1 << 64) - 1
_MAX_RECORD_VALUE_BYTES = 1_048_576


class ActiveLeaseError(ManifestError):
    """The source service must be stopped or its outstanding lease must expire."""


def _utc(value):
    """Normalize a UTC source timestamp without consulting runner local time.

    MariaDB DATETIME values are written by this migration's UTC-only source
    contract and DB-API drivers commonly decode them as naive ``datetime``.
    Treat that decoded representation as UTC explicitly.  Text values, in
    contrast, must carry an offset; accepting a naive string would make the
    result depend on the maintenance runner's local timezone.
    """
    if value is None:
        return None
    if isinstance(value, datetime):
        if value.tzinfo is None:
            return value.replace(tzinfo=timezone.utc)
        return value.astimezone(timezone.utc)
    if not isinstance(value, str) or not value:
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ManifestError("source lease timestamp is invalid") from error
    if parsed.tzinfo is None:
        raise ManifestError("source timestamp text must include a UTC offset")
    return parsed.astimezone(timezone.utc)


def _coverage_entry(row, manifest_id, service, table, reason):
    return {
        "classification": "coverage_gap", "classification_reason": reason,
        "event_id": None, "manifest_id": manifest_id, "payload_hash": None,
        "producer_epoch": None, "producer_id": None, "producer_sequence": None,
        "producer_stream_id": None, "source_primary_key": str(row["outbox_id"]),
        "source_service": service, "source_status": row["status"], "source_table": table,
    }


def _uses_base_stream(stream, base_stream):
    """Accept the legacy base stream or the frozen ``base:boot`` form only."""
    return stream == base_stream or (
        stream.startswith(base_stream + ":") and len(stream) > len(base_stream) + 1
    )


def classify_row(row, manifest_id, snapshot_at):
    """Return the immutable manifest entry and original Event value for one row.

    The caller supplies rows from a transactionally frozen source snapshot. This
    function neither mutates the source nor obtains any central-DB capability.
    """
    # All rows must share one valid UTC cutover instant; checking it here keeps
    # even non-lease rows from silently accepting an unfrozen caller boundary.
    if _utc(snapshot_at) is None:
        raise ManifestError("source snapshot timestamp is invalid")
    table = row.get("source_table")
    source = _SOURCES.get(table)
    if source is None or not isinstance(manifest_id, str) or not manifest_id:
        raise ManifestError("source table or manifest ID is invalid")
    service, expected_producer_id, base_stream = source
    if not isinstance(row.get("outbox_id"), int) or row["outbox_id"] <= 0:
        raise ManifestError("source primary key is invalid")
    status = row.get("status")
    if not isinstance(status, str):
        raise ManifestError("source status is invalid")
    if status == "delivered":
        classification, reason = "verify_delivered", "delivered"
    elif status in _PUBLISH:
        classification, reason = "publish", status
    elif status == "processing":
        lease = _utc(row.get("locked_until"))
        now = _utc(snapshot_at)
        if now is None:
            raise ManifestError("source snapshot timestamp is invalid")
        if lease is None:
            raise ActiveLeaseError("missing source lease cannot prove expiry")
        if lease > now:
            raise ActiveLeaseError("active source lease prevents migration snapshot")
        classification, reason = "publish", "expired_lease"
    elif status in _COVERAGE_GAP:
        return _coverage_entry(row, manifest_id, service, table, status), None
    else:
        return _coverage_entry(row, manifest_id, service, table, "unknown_status"), None

    try:
        stored = json.loads(row["envelope"])
        event = stored["event"]
    except (KeyError, TypeError, json.JSONDecodeError):
        return _coverage_entry(row, manifest_id, service, table, "bad_payload"), None
    if not isinstance(event, dict) or any(
        not isinstance(event.get(key), str) or not event[key]
        for key in _STRING_IDENTITY
    ) or any(
        type(event.get(key)) is not int or not 0 < event[key] <= _UINT64_MAX
        for key in _NUMERIC_IDENTITY
    ):
        return _coverage_entry(row, manifest_id, service, table, "bad_payload"), None
    if any(row.get(key) != event[key] for key in _STRING_IDENTITY) or any(
        type(row.get(key)) is not int or row[key] != event[key]
        for key in _NUMERIC_IDENTITY
    ):
        return _coverage_entry(row, manifest_id, service, table, "source_identity_mismatch"), None
    if event["producer_id"] != expected_producer_id or not _uses_base_stream(event["producer_stream_id"], base_stream):
        return _coverage_entry(row, manifest_id, service, table, "source_identity_mismatch"), None
    value_bytes = json.dumps(event, separators=(",", ":"), ensure_ascii=False).encode("utf-8")
    if len(value_bytes) > _MAX_RECORD_VALUE_BYTES:
        return _coverage_entry(row, manifest_id, service, table, "bad_payload"), None
    return {
        "classification": classification, "classification_reason": reason,
        "event_id": event["event_id"], "manifest_id": manifest_id, "payload_hash": event["payload_hash"],
        "producer_epoch": str(event["producer_epoch"]), "producer_id": event["producer_id"],
        "producer_sequence": str(event["producer_sequence"]), "producer_stream_id": event["producer_stream_id"],
        "source_primary_key": str(row["outbox_id"]), "source_service": service,
        "source_status": status, "source_table": table,
    }, event


def encode_migration_record(entry, event, producer_instance_id):
    """Encode the frozen Event value with the exact migration-only Header set."""
    if entry.get("classification") != "publish" or not isinstance(event, dict):
        raise ManifestError("only a publish entry with an Event can be encoded")
    stream = event.get("producer_stream_id")
    if not isinstance(stream, str) or not stream or not isinstance(producer_instance_id, str) or not producer_instance_id:
        raise ManifestError("migration Record identity is invalid")
    return {
        "key": stream,
        "value": json.dumps(event, separators=(",", ":"), ensure_ascii=False).encode("utf-8"),
        "headers": {
            "content-type": "application/json",
            "bkn-trace-schema-version": "3.0.0",
            "capture_policy_revision": "0",
            "producer_instance_id": producer_instance_id,
            "bkn-evidence-record-class": "migration",
            "bkn-evidence-migration-id": entry["manifest_id"],
        },
    }
