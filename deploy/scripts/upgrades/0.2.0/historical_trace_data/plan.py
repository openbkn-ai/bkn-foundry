"""Freeze native candidates and explicit dispositions before any target writes."""
import json
import subprocess
import uuid
from collections import Counter
from pathlib import Path

from logs import convert_log
from trace import convert_evidence, convert_span
from snapshot import canonical, digest, private_write, read_snapshot, strict_loads


def native_validate(requests, executable):
    if not requests:
        return []
    result = subprocess.run([str(executable)], input="".join(canonical(row) + "\n" for row in requests),
                            capture_output=True, text=True)
    if result.returncode:
        raise ValueError("native validator process failed")
    outputs = [strict_loads(line) for line in result.stdout.splitlines()]
    if len(outputs) != len(requests):
        raise ValueError("native validator response count mismatch")
    for request, output in zip(requests, outputs):
        if not isinstance(output, dict) or type(output.get("accepted")) is not bool:
            raise ValueError("native validator response schema invalid")
        if output["accepted"] and canonical(output.get("canonical_payload")) != canonical(request["payload"]):
            raise ValueError("native validator payload mismatch")
    return outputs


def expand_otlp(document, deployment, executable):
    raw = document["_source"]
    if isinstance(raw, str):
        raw = strict_loads(raw)
    process = subprocess.run([str(executable)], input=canonical({"document": raw, "source_deployment": deployment}) + "\n", capture_output=True, text=True)
    if process.returncode:
        raise ValueError("official span codec process failed")
    replies = [strict_loads(line) for line in process.stdout.splitlines()]
    if len(replies) != 1:
        raise ValueError("official span codec response count mismatch")
    reply = replies[0]
    if not isinstance(reply, dict) or type(reply.get("accepted")) is not bool or not isinstance(reply.get("spans"), list):
        raise ValueError("official span codec response schema invalid")
    if not reply["accepted"]:
        return [{"disposition": "blocked", "reason": reply.get("reason", "official_codec_rejected"), "sidecar": {}}]
    results = []
    for span in reply.get("spans", []):
        payload = span["payload"]
        candidate = convert_span({"index": document["index"], "id": document["id"] + ":" + payload["traceId"] + ":" + payload["spanId"], "routing": document.get("routing"), "_source": payload}, deployment)
        candidate.setdefault("sidecar", {})["otlp_mapping"] = span["sidecar"]
        candidate["sidecar"]["target_id"] = str(uuid.uuid5(uuid.UUID("c7e7aeb8-7a0b-58f7-b854-eb686f12cb65"), canonical([deployment, payload["traceId"], payload["spanId"]])))
        results.append(candidate)
    return results or [{"disposition": "archive", "reason": "otlp_document_has_no_spans", "sidecar": {}}]


def create(source, output, environment, broker_time, validator, span_codec=None):
    source = Path(source)
    records = list(read_snapshot(source))
    snapshot_manifest = strict_loads((source / "snapshot.json").read_text())
    deployment = snapshot_manifest["source_deployment"]
    items, requests, pending = [], [], []
    for source_ordinal, record in enumerate(records):
        kind = record["kind"]
        row = record.get("row", record.get("document"))
        if kind == "audit":
            converted = convert_log(record["source_id"], row, environment, deployment)
            payload = converted.get("event")
        elif kind == "evidence":
            converted = convert_evidence(row, deployment)
            payload = converted.get("payload")
        elif kind == "span":
            converted = convert_span(row, deployment)
            payload = converted.get("payload")
        else:
            converted = {"disposition": "blocked", "reason": "unknown_source_kind"}
            payload = None
        expanded = expand_otlp(row, deployment, span_codec) if kind == "span" and converted.get("reason") == "official_otlp_codec_required" and span_codec else [converted]
        for candidate in expanded:
            payload = candidate.get("event") if kind == "audit" else candidate.get("payload")
            item = {"ordinal": len(items), "source_ordinal": source_ordinal, "source_id": record["source_id"], "kind": kind,
                    "source_sha256": digest(canonical(record).encode()), "source": record,
                    "disposition": candidate["disposition"], "reason": candidate.get("reason", ""),
                    "sidecar": candidate.get("sidecar", {})}
            if item["disposition"] == "convert":
                if not isinstance(payload, dict):
                    raise ValueError("converter returned invalid native payload")
                pending.append(len(items))
                requests.append({"kind": kind, "payload": payload, "broker_time": broker_time})
            items.append(item)
    results = native_validate(requests, validator)
    identities = {}
    for index, result in zip(pending, results):
        item = items[index]
        if not result.get("accepted"):
            item.update(disposition="blocked", reason="native_rejected:" + result.get("reason", "unknown"))
            continue
        payload = result["canonical_payload"]
        item.update(payload=payload, native_validation=result["reason"], content_hash=result["content_hash"],
                    payload_sha256=digest(canonical(payload).encode()))
        identity = result.get("event_id")
        if item["kind"] == "span":
            item["target_id"] = item["sidecar"]["target_id"]
            identity = (payload["traceId"], payload["spanId"])
        if identity:
            if item["kind"] != "span":
                item["target_id"] = identity
            key = item["kind"], identity
            indices = identities.setdefault(key, [])
            indices.append(index)
            if len({items[position]["content_hash"] for position in indices}) > 1:
                for position in indices:
                    items[position].update(disposition="blocked", reason="source_identity_content_conflict")
    encoded = "".join(canonical(item) + "\n" for item in items).encode()
    manifest = {"format_version": 1, "stage": "native_format_plan", "scope_policy": "current_020_operation_logs",
                "source_snapshot_sha256": digest((source / "snapshot.json").read_bytes()),
                "source_deployment": deployment, "environment": environment, "validation_time": broker_time,
                "items_sha256": digest(encoded), "record_count": len(records), "item_count": len(items),
                "counts": dict(sorted(Counter(item["disposition"] for item in items).items())),
                "reasons": dict(sorted(Counter(item["reason"] for item in items).items())),
                "target_commit": snapshot_manifest.get("metadata", {}).get("target_pin"),
                "write_admission": "not_qualified"}
    output = Path(output)
    output.mkdir(mode=0o700, parents=True, exist_ok=False)
    private_write(output / "items.jsonl", encoded)
    private_write(output / "plan.json", (canonical(manifest) + "\n").encode())
    private_write(output / "report.md", ("# Historical Conversion Plan\n\n" +
        "Native format checks only; no target writes or query proof.\n\n" +
        "\n".join("- %s: %s" % entry for entry in manifest["counts"].items()) + "\n").encode())
    return manifest


def verify(root):
    root = Path(root)
    manifest = strict_loads((root / "plan.json").read_text())
    payload = (root / "items.jsonl").read_bytes()
    if manifest.get("format_version") != 1 or digest(payload) != manifest["items_sha256"]:
        raise ValueError("plan hash mismatch")
    items = [strict_loads(line) for line in payload.splitlines()]
    counts = dict(sorted(Counter(item["disposition"] for item in items).items()))
    if len(items) != manifest.get("item_count", manifest["record_count"]) or counts != manifest["counts"]:
        raise ValueError("plan count mismatch")
    return manifest
