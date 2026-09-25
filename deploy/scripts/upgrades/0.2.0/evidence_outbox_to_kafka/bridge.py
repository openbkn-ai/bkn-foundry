"""Restart-safe, publish-only bridge core; Kafka transport is injected."""

import json
import os
from pathlib import Path

from manifest import ManifestError, verify_activation


def write_checkpoint(path, checkpoint):
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    temp = target.with_name(target.name + ".tmp")
    with temp.open("w", encoding="utf-8") as stream:
        json.dump(checkpoint, stream, sort_keys=True, separators=(",", ":"))
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, target)
    directory = os.open(str(target.parent), os.O_RDONLY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


def load_checkpoint(path, manifest_id, source_snapshot_at):
    target = Path(path)
    if not target.exists():
        return None
    try:
        value = json.loads(target.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None  # safe full rescan, never a skip
    if value.get("manifest_id") != manifest_id or value.get("source_snapshot_at") != source_snapshot_at:
        raise ManifestError("bridge checkpoint belongs to another manifest or source snapshot")
    return value


def publish_entries(manifest, entries, checkpoint_path, publish):
    """Publish only immutable `publish` entries and checkpoint after each ACK."""
    verify_activation(manifest, entries)
    checkpoint = load_checkpoint(checkpoint_path, manifest["manifest_id"], manifest["source_snapshot_at"])
    start_after = None if checkpoint is None else checkpoint.get("entry_id")
    emitted = []
    for entry in sorted(entries, key=lambda row: (row["source_table"], row["source_primary_key"])):
        if entry["classification"] != "publish":
            continue
        if start_after is not None and entry["entry_id"] <= start_after:
            continue
        publish(entry)  # raises on no Kafka ACK; checkpoint then remains behind
        write_checkpoint(checkpoint_path, {"manifest_id": manifest["manifest_id"], "source_snapshot_at": manifest["source_snapshot_at"], "entry_id": entry["entry_id"]})
        emitted.append(entry["entry_id"])
    return emitted
