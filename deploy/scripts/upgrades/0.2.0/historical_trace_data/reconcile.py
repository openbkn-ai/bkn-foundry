"""Native Audit readback. Kafka ACKs never count as persistence proof."""
import json
import re
from collections import Counter
from datetime import datetime, timezone

from snapshot import canonical, identifier, literal


def check(items, fetch):
    results = []
    for item in items:
        if item["disposition"] != "convert":
            continue
        if item["kind"] != "audit":
            results.append({"ordinal": item["ordinal"] if "ordinal" in item else None, "status": "not_checked"})
            continue
        row = fetch(item)
        if row is None:
            status = "missing"
        elif row.get("content_hash") != item["content_hash"] or row.get("dedup_hash") != item["content_hash"] or canonical(row.get("payload")) != canonical(item["payload"]) or not _facts_match(row, item):
            status = "conflict"
        else:
            status = "verified"
        results.append({"ordinal": item.get("ordinal"), "status": status})
    counts = dict(sorted(Counter(row["status"] for row in results).items()))
    return {"complete": bool(results) and all(row["status"] == "verified" for row in results), "counts": counts, "results": results}


def _facts_match(row, item):
    try:
        occurred = datetime.fromisoformat(item["payload"]["occurred_at"].replace("Z", "+00:00")).astimezone(timezone.utc)
        actual = datetime.fromisoformat(row["occurred_at"].replace("Z", "+00:00"))
    except (KeyError, TypeError, ValueError):
        return False
    return (actual == occurred and row.get("target_table") == "audit_event_" + occurred.strftime("%Y%m") and
            row.get("source_id") == item["source_id"] and row.get("topic") == "openbkn.audit.v1" and
            type(row.get("partition")) is int and row["partition"] >= 0 and
            type(row.get("offset")) is int and row["offset"] >= 0)


def fetch_audit(source, item):
    event_id = item["target_id"]
    if not re.fullmatch(r"[0-9a-f-]{36}", event_id):
        raise ValueError("invalid native event identity")
    rows = source.query("SELECT JSON_OBJECT('target_table',target_table,'dedup_hash',content_hash) FROM bkn_audit.audit_event_dedup WHERE event_id=" + literal(event_id))
    if not rows:
        return None
    if len(rows) != 1:
        raise ValueError("native dedup identity conflict")
    row = json.loads(rows[0])
    table = row["target_table"]
    if not re.fullmatch(r"audit_event_[0-9]{6}", table):
        raise ValueError("invalid native month table")
    payloads = source.query("SELECT JSON_OBJECT('content_hash',content_hash,'payload',JSON_QUERY(payload,'$'),'source_id',source_id,'occurred_at',DATE_FORMAT(occurred_at,'%Y-%m-%dT%H:%i:%s.%fZ'),'topic',topic,'partition',partition_id,'offset',offset_id) FROM bkn_audit." + identifier(table) + " WHERE event_id=" + literal(event_id))
    if not payloads:
        return None
    if len(payloads) != 1:
        raise ValueError("native month identity conflict")
    row.update(json.loads(payloads[0]))
    return row
