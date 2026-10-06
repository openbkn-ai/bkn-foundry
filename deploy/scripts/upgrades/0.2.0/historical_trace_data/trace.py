"""Pure offline Trace extraction; no database, broker, or index capabilities.

SHA-256 metadata hashes the deterministic Python JSON representation, not JCS
or the Ledger's Go canonical payload. Ledger validation remains mandatory.
"""

import copy
from datetime import datetime, timedelta, timezone
import hashlib
import json
import re
import uuid


_IDENTITY = ("event_id", "payload_hash", "producer_id", "producer_stream_id",
             "producer_epoch", "producer_sequence")
_SCHEMA = "bkn.trace.schema.version"
_UINT64_MAX = (1 << 64) - 1
_RFC3339 = re.compile(
    r"^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$"
)
_SPAN_NAMESPACE = uuid.UUID("c7e7aeb8-7a0b-58f7-b854-eb686f12cb65")


def _encode(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":"),
                      ensure_ascii=False, allow_nan=False).encode("utf-8")


def _hash(value):
    return hashlib.sha256(_encode(value)).hexdigest()


def _pairs(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON key")
        result[key] = value
    return result


def _decode(value):
    if isinstance(value, str):
        def reject_constant(_):
            raise ValueError("nonfinite JSON value")
        return json.loads(value, object_pairs_hook=_pairs, parse_constant=reject_constant)
    _encode(value)
    return copy.deepcopy(value)


def _result(disposition, reason, kind, sidecar, payload=None):
    if payload is not None:
        sidecar["native_sha256"] = _hash(payload)
    return {"disposition": disposition, "reason": reason, "kind": kind,
            "payload": payload, "sidecar": sidecar}


def _sidecar(value, deployment):
    sidecar = {"source_deployment": deployment, "hash_codec": "python-json-sorted-v1"}
    try:
        sidecar["source_sha256"] = _hash(value)
        sidecar["source"] = copy.deepcopy(value)
    except (ValueError, TypeError, UnicodeError):
        sidecar["source_sha256"] = None
    return sidecar


def convert_evidence(row, source_deployment):
    """Extract the original native Event; caller must validate it with Go.

    This is a format decision, not permission to replay pending work or an
    admission/watermark/ownership verdict. Status and source row remain sidecar.
    """
    sidecar = _sidecar(row, source_deployment)
    if not isinstance(row, dict) or not isinstance(source_deployment, str) or not source_deployment:
        return _result("blocked", "invalid_source_locator", "evidence", sidecar)
    try:
        stored = _decode(row.get("envelope"))
    except (ValueError, TypeError, UnicodeError):
        return _result("archive", "invalid_source_json", "evidence", sidecar)
    event = stored.get("event") if isinstance(stored, dict) else None
    if not isinstance(event, dict):
        return _result("archive", "invalid_evidence_wrapper", "evidence", sidecar)
    if event.get(_SCHEMA) != "3.0.0":
        return _result("blocked", "unsupported_evidence_schema", "evidence", sidecar)
    if "schema_version" in row and row["schema_version"] != event[_SCHEMA]:
        return _result("blocked", "source_schema_mismatch", "evidence", sidecar)
    for key in _IDENTITY:
        value = event.get(key)
        if key in ("producer_epoch", "producer_sequence"):
            valid = type(value) is int and 0 < value <= _UINT64_MAX
            matches = key not in row or (type(row[key]) is int and row[key] == value)
        else:
            valid = isinstance(value, str) and bool(value)
            matches = row.get(key) == value if key in ("event_id", "payload_hash") else key not in row or row[key] == value
        if not valid or not matches:
            return _result("blocked", "source_identity_mismatch", "evidence", sidecar)
    if re.fullmatch(r"[0-9a-f]{64}", event["payload_hash"]) is None:
        return _result("archive", "invalid_payload_hash", "evidence", sidecar)
    sidecar.update({"validation_required": "native_ledger", "ledger_payload_hash": event["payload_hash"]})
    return _result("convert", "native_event_extracted", "evidence", sidecar, event)


def _id_valid(value, length, allow_empty=False):
    if allow_empty and value in (None, "", "0" * length):
        return True
    return (isinstance(value, str) and re.fullmatch(r"[0-9a-f]{%d}" % length, value) is not None
            and value != "0" * length)


def _timestamp_ns(value):
    if not isinstance(value, str):
        raise ValueError("timestamp must be text")
    match = _RFC3339.fullmatch(value)
    if match is None:
        raise ValueError("invalid RFC3339 timestamp")
    date, clock, fraction, offset = match.groups()
    zone = timezone.utc
    if offset != "Z":
        hours, minutes = int(offset[1:3]), int(offset[4:6])
        if hours > 23 or minutes > 59:
            raise ValueError("invalid UTC offset")
        sign = 1 if offset[0] == "+" else -1
        zone = timezone(sign * timedelta(hours=hours, minutes=minutes))
    parsed = datetime.fromisoformat(date + "T" + clock).replace(tzinfo=zone)
    delta = parsed - datetime(1970, 1, 1, tzinfo=timezone.utc)
    return (delta.days * 86400 + delta.seconds) * 1_000_000_000 + int((fraction or "").ljust(9, "0"))


def _native_shape(value):
    for field in ("attributes", "resource", "instrumentationScope", "status"):
        if field in value and not isinstance(value[field], dict):
            raise ValueError("invalid native object")
    for field in ("name", "kind", "traceState", "schemaUrl", "version", "code", "message"):
        if field in value and not isinstance(value[field], str):
            raise ValueError("invalid native text")
    for field in ("droppedAttributesCount", "droppedEventsCount", "droppedLinksCount"):
        if field in value and (type(value[field]) is not int or not 0 <= value[field] <= (1 << 32) - 1):
            raise ValueError("invalid dropped count")
    if any(not isinstance(item, str) for item in value.get("resource", {}).values()):
        raise ValueError("resource values must be strings")
    for field in ("instrumentationScope", "status"):
        if field in value:
            _native_shape(value[field])


def convert_span(document, source_deployment):
    """Preserve complete native SS4O documents; never hand-roll OTLP flattening."""
    sidecar = _sidecar(document, source_deployment)
    if (not isinstance(document, dict) or not isinstance(source_deployment, str) or not source_deployment
            or not isinstance(document.get("index"), str) or not document["index"]
            or not isinstance(document.get("id"), str) or not document["id"]):
        return _result("blocked", "invalid_source_locator", "span", sidecar)
    sidecar.update({"source_index": document["index"], "source_id": document["id"],
                    "routing": document.get("routing")})
    try:
        payload = _decode(document.get("_source"))
    except (ValueError, TypeError, UnicodeError):
        return _result("archive", "invalid_source_json", "span", sidecar)
    if not isinstance(payload, dict):
        return _result("archive", "invalid_span_document", "span", sidecar)
    if "resourceSpans" in payload:
        return _result("blocked", "official_otlp_codec_required", "span", sidecar)
    if not (_id_valid(payload.get("traceId"), 32) and _id_valid(payload.get("spanId"), 16)
            and _id_valid(payload.get("parentSpanId"), 16, allow_empty=True)):
        return _result("archive", "invalid_span_identity", "span", sidecar)
    try:
        _native_shape(payload)
        start, end = _timestamp_ns(payload.get("startTime")), _timestamp_ns(payload.get("endTime"))
        if end < start:
            raise ValueError("negative duration")
        events = payload.get("events", [])
        links = payload.get("links", [])
        if not isinstance(events, list) or not isinstance(links, list):
            raise ValueError("events and links must be arrays")
        for event in events:
            if not isinstance(event, dict):
                raise ValueError("invalid event")
            _native_shape(event)
            for field in ("@timestamp", "observedTimestamp"):
                if field in event:
                    _timestamp_ns(event[field])
        for link in links:
            if not isinstance(link, dict) or not _id_valid(link.get("traceId"), 32) or not _id_valid(link.get("spanId"), 16):
                raise ValueError("invalid link identity")
            _native_shape(link)
    except (ValueError, TypeError, OverflowError):
        return _result("archive", "invalid_span_fields", "span", sidecar)
    locator = [source_deployment, document["index"], document["id"]]
    sidecar.update({"target_id": str(uuid.uuid5(_SPAN_NAMESPACE, _encode(locator).decode("utf-8"))),
                    "identity_rule": "source-qualified-document-v1",
                    "validation_required": "target_mapping_and_business_scope"})
    return _result("convert", "native_ss4o_identity", "span", sidecar, payload)
