"""Frozen C1 manifest canonicalization and lifecycle checks.

This module intentionally accepts the RFC 8785 ASCII scalar subset v1 only.
It is not a general JSON canonicalizer: allowing JSON numbers or non-ASCII
values would silently change the contract's digest domain.
"""

import hashlib
import json

CONTRACT_SHA = "0016ad359b11d162e04bb11a78784c33fad0ec8d"
ENTRY_FIELDS = {
    "classification", "classification_reason", "event_id", "manifest_id",
    "payload_hash", "producer_epoch", "producer_id", "producer_sequence",
    "producer_stream_id", "source_primary_key", "source_service",
    "source_status", "source_table",
}
RESULT_FIELDS = {
    "adjudication", "entry_id", "first_observation", "first_observed_at",
    "kafka_offset", "kafka_partition", "kafka_topic", "last_observation",
    "last_observed_at", "ledger_ingest_sequence", "manifest_id", "reason_code",
}


class ManifestError(ValueError):
    pass


def _ascii_subset(value):
    if value is None or isinstance(value, bool):
        return
    if isinstance(value, str):
        if any(ord(char) < 0x20 or ord(char) > 0x7E for char in value):
            raise ManifestError("only printable ASCII strings are permitted")
        return
    if isinstance(value, (int, float)):
        raise ManifestError("JSON numbers are outside RFC 8785 ASCII scalar subset v1")
    if isinstance(value, list):
        for item in value:
            _ascii_subset(item)
        return
    if isinstance(value, dict):
        for key, item in value.items():
            if not isinstance(key, str):
                raise ManifestError("JSON object keys must be strings")
            _ascii_subset(key)
            _ascii_subset(item)
        return
    raise ManifestError("unsupported canonical manifest value")


def canonical_bytes(value):
    _ascii_subset(value)
    return json.dumps(value, sort_keys=True, ensure_ascii=True, separators=(",", ":")).encode("ascii")


def digest(value):
    return hashlib.sha256(canonical_bytes(value)).hexdigest()


def entries_digest(entries):
    for entry in entries:
        if set(entry) not in (ENTRY_FIELDS, ENTRY_FIELDS | {"entry_id"}):
            raise ManifestError("manifest entry fields do not match the frozen contract")
    # entry_id is a database locator, not an immutable digest field.
    canonical_entries = [{key: entry[key] for key in ENTRY_FIELDS} for entry in entries]
    ordered = sorted(canonical_entries, key=lambda entry: (entry["source_service"], entry["source_table"], entry["source_primary_key"]))
    return digest(ordered)


def verify_activation(manifest, entries):
    if manifest.get("contract_sha") != CONTRACT_SHA or manifest.get("state") not in ("draft", "active"):
        raise ManifestError("manifest contract or activation state is invalid")
    computed = entries_digest(entries)
    if manifest.get("entry_count") != str(len(entries)) or manifest.get("entries_digest") != computed:
        raise ManifestError("manifest issuance does not match immutable entries")
    return computed


def verify_active_runtime(manifest, entries):
    """Validate an already issued manifest before bridge publication."""
    verify_activation(manifest, entries)
    if manifest.get("state") != "active":
        raise ManifestError("bridge requires an active immutable manifest")


def closure_digest(manifest_header, results):
    required_header = {"activated_at", "closed_at", "contract_sha", "entries_digest", "entry_count", "manifest_id", "source_snapshot_at", "terminal_counts"}
    if set(manifest_header) != required_header or manifest_header.get("contract_sha") != CONTRACT_SHA:
        raise ManifestError("closure manifest header is invalid")
    for result in results:
        if set(result) != RESULT_FIELDS:
            raise ManifestError("closure result fields do not match the frozen contract")
    return digest({"manifest_header": manifest_header, "results": sorted(results, key=lambda result: result["entry_id"])})
