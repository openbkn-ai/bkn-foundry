"""Restart-safe, active-manifest, publish-only bridge core."""

import json
import os
from pathlib import Path

from manifest import ManifestError, verify_active_runtime


def _fault(fault, stage):
    if fault is not None:
        fault(stage)


def write_checkpoint(path, checkpoint, fault=None):
    """Persist the C1 protocol; hooks make every crash boundary testable."""
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    temp = target.with_name(target.name + ".tmp")
    with temp.open("w", encoding="utf-8") as stream:
        json.dump(checkpoint, stream, sort_keys=True, separators=(",", ":"))
        stream.flush()
        _fault(fault, "after_temp_write")
        os.fsync(stream.fileno())
        _fault(fault, "after_file_fsync")
    os.replace(temp, target)
    _fault(fault, "after_rename")
    directory = os.open(str(target.parent), os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)
    _fault(fault, "after_directory_fsync")


def load_checkpoint(path, manifest_id, source_snapshot_at):
    target = Path(path)
    if not target.exists():
        return None
    try:
        value = json.loads(target.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    required = {"manifest_id", "source_snapshot_at", "source_table", "source_primary_key", "source_status", "event_identity", "last_kafka_ack", "classification_counts", "completed"}
    if set(value) != required:
        return None
    if value["manifest_id"] != manifest_id or value["source_snapshot_at"] != source_snapshot_at:
        raise ManifestError("bridge checkpoint belongs to another manifest or source snapshot")
    return value


def _source_cursor(entry):
    return entry["source_table"], entry["source_primary_key"]


def _checkpoint(manifest, entry, ack, counts, completed=False):
    return {
        "manifest_id": manifest["manifest_id"], "source_snapshot_at": manifest["source_snapshot_at"],
        "source_table": entry["source_table"], "source_primary_key": entry["source_primary_key"],
        "source_status": entry["source_status"],
        "event_identity": {key: entry[key] for key in ("event_id", "payload_hash", "producer_id", "producer_stream_id", "producer_epoch", "producer_sequence")},
        "last_kafka_ack": ack, "classification_counts": dict(counts), "completed": completed,
    }


def publish_entries(manifest, entries, checkpoint_path, publish, fault=None):
    """Publish only `publish` entries; source table/PK is the recovery cursor."""
    verify_active_runtime(manifest, entries)
    checkpoint = load_checkpoint(checkpoint_path, manifest["manifest_id"], manifest["source_snapshot_at"])
    start_after = None if checkpoint is None else (checkpoint["source_table"], checkpoint["source_primary_key"])
    counts = {} if checkpoint is None else dict(checkpoint["classification_counts"])
    last_ack = None if checkpoint is None else checkpoint["last_kafka_ack"]
    last_entry = None
    emitted = []
    for entry in sorted(entries, key=_source_cursor):
        if start_after is not None and _source_cursor(entry) <= start_after:
            continue
        counts[entry["classification"]] = counts.get(entry["classification"], 0) + 1
        last_entry = entry
        if entry["classification"] != "publish":
            continue
        ack = publish(entry)
        if not isinstance(ack, dict) or not {"topic", "partition", "offset"}.issubset(ack):
            raise ManifestError("bridge publish callback must return a Kafka ACK coordinate")
        last_ack = ack
        write_checkpoint(checkpoint_path, _checkpoint(manifest, entry, ack, counts), fault)
        # The source-only frozen artifact deliberately has no central DB
        # entry_id.  Its durable identity is the C1 source cursor; an admin
        # may add entry_id for its own store without granting it to bridge.
        emitted.append(entry.get("entry_id", _source_cursor(entry)))
    # A trailing verify/coverage classification has no ACK of its own. Persist
    # a completed source cursor with the last ACK so classification accounting
    # is durable even when no later publish record exists.
    if last_entry is not None:
        write_checkpoint(checkpoint_path, _checkpoint(manifest, last_entry, last_ack, counts, completed=True), fault)
    return emitted


def publish_encoded_entries(manifest, entries, events_by_source_cursor, checkpoint_path,
                            producer, topic, timeout_seconds, producer_instance_id,
                            fault=None):
    """Publish frozen migration Events through the injected Kafka ACK adapter.

    `events_by_source_cursor` is an in-memory result of the source-only
    snapshot reader.  The function deliberately has no central-store argument:
    manifest issuance/activation and reconciliation are separate, controlled
    commands.  A missing or identity-mismatched Event fails before any broker
    send so a checkpoint can never advance over an unverified source row.
    """
    from kafka import publish_with_ack
    from source import encode_migration_record

    def publish(entry):
        cursor = _source_cursor(entry)
        event = events_by_source_cursor.get(cursor)
        if not isinstance(event, dict):
            raise ManifestError("frozen publish entry has no source Event")
        identity = (
            "event_id", "payload_hash", "producer_id", "producer_stream_id",
            "producer_epoch", "producer_sequence",
        )
        if any(str(event.get(key)) != str(entry.get(key)) for key in identity):
            raise ManifestError("frozen source Event does not match manifest entry")
        record = encode_migration_record(entry, event, producer_instance_id)
        return publish_with_ack(producer, topic, record, timeout_seconds)

    return publish_entries(manifest, entries, checkpoint_path, publish, fault)
