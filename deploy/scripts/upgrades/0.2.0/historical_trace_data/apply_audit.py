"""Publish only approved Audit bytes; Kafka ACK is not database confirmation."""

import ipaddress
import os
from pathlib import Path
import subprocess
import tempfile
from urllib.parse import urlsplit

from plan import verify
from snapshot import canonical, digest, strict_loads


_SOURCES = {"bkn-backend", "vega", "execution-factory", "model-manager", "bkn-safe-admin", "bkn-safe-access"}


def audit_profile():
    return {"brokers": [value.strip() for value in os.environ.get("BKN_HISTORY_KAFKA_BROKERS", "").split(",") if value.strip()],
            "mechanism": os.environ.get("BKN_HISTORY_KAFKA_MECHANISM", ""),
            "username_env": "BKN_HISTORY_KAFKA_USERNAME", "password_env": "BKN_HISTORY_KAFKA_PASSWORD"}


def _loopback(host):
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except (TypeError, ValueError):
        return False


def prepare_requests(plan_root, expected_items_sha256, selected_sources, *, expected_plan_sha256=None):
    """Return exact approved Audit-only NDJSON and non-sensitive counters."""
    if (not isinstance(selected_sources, (list, tuple, set)) or not selected_sources or
            any(not isinstance(source, str) or source not in _SOURCES for source in selected_sources)):
        raise ValueError("explicit registered Audit source selection required")
    selected = set(selected_sources)
    manifest_data = (Path(plan_root) / "plan.json").read_bytes()
    if digest(manifest_data) != expected_plan_sha256:
        raise ValueError("complete plan manifest SHA-256 approval required")
    manifest = verify(plan_root)
    if (Path(plan_root) / "plan.json").read_bytes() != manifest_data:
        raise ValueError("approved plan manifest changed")
    if expected_items_sha256 != manifest["items_sha256"]:
        raise ValueError("approved items SHA-256 mismatch")
    data = (Path(plan_root) / "items.jsonl").read_bytes()
    if digest(data) != expected_items_sha256:
        raise ValueError("approved plan changed")
    clock = manifest.get("validation_time")
    if not isinstance(clock, str) or not clock:
        raise ValueError("plan validation time missing")
    summary = {"publish_count": 0, "archive_count": 0, "unselected_audit_count": 0,
               "other_writer_count": 0, "selected_sources": sorted(selected)}
    requests, identities = [], {}
    for line in data.splitlines():
        item = strict_loads(line)
        if item.get("kind") != "audit":
            summary["other_writer_count"] += 1
            continue
        if item.get("source_id") not in selected:
            summary["unselected_audit_count"] += 1
            continue
        source = item.get("source")
        if (not isinstance(source, dict) or source.get("source_id") != item["source_id"] or
                source.get("kind") != "audit" or digest(canonical(source).encode()) != item.get("source_sha256")):
            raise ValueError("selected Audit source identity or hash mismatch")
        disposition = item.get("disposition")
        if disposition == "blocked":
            raise ValueError("selected Audit source contains blocked items")
        if disposition == "archive":
            summary["archive_count"] += 1
            continue
        if disposition != "convert":
            raise ValueError("unsupported selected Audit disposition")
        payload = item.get("payload")
        if not isinstance(payload, dict) or payload.get("source_id") != item["source_id"]:
            raise ValueError("selected Audit payload source mismatch")
        checksum = digest(canonical(payload).encode())
        if item.get("payload_sha256") != checksum or item.get("content_hash") != "sha256:" + checksum:
            raise ValueError("selected Audit payload or native content hash mismatch")
        event_id = payload.get("event_id")
        if not isinstance(event_id, str) or not event_id or event_id != item.get("target_id"):
            raise ValueError("selected Audit target identity mismatch")
        previous = identities.get(event_id)
        if previous is not None and previous != checksum:
            raise ValueError("selected Audit UUID content conflict")
        identities[event_id] = checksum
        requests.append({"kind": "audit", "payload": payload, "broker_time": clock})
        summary["publish_count"] += 1
    encoded = "".join(canonical(request) + "\n" for request in requests).encode()
    return encoded, summary


def _fsync_directory(path):
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _write_receipt(path, value, initial=False):
    data = (canonical(value) + "\n").encode()
    if initial:
        try:
            fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        except FileExistsError:
            raise ValueError("receipt already exists; reconcile previous outcome before a fresh explicit attempt") from None
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
    else:
        fd, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
        try:
            with os.fdopen(fd, "wb") as stream:
                stream.write(data)
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    _fsync_directory(path.parent)


def _confirmed_ack(reply, request):
    if not isinstance(reply, dict):
        return False
    payload = request["payload"]
    kafka = reply.get("kafka")
    return (reply.get("accepted") is True and
            reply.get("reason") == "kafka_ack_not_database_confirmation" and
            reply.get("event_id") == payload["event_id"] and
            reply.get("content_hash") == "sha256:" + digest(canonical(payload).encode()) and
            isinstance(kafka, dict) and kafka.get("topic") == "openbkn.audit.v1" and
            type(kafka.get("partition")) is int and kafka["partition"] >= 0 and
            type(kafka.get("offset")) is int and kafka["offset"] >= 0)


def apply(plan_root, expected_items_sha256, sources, validator, receipt_path, *,
          expected_plan_sha256=None, expected_profile_sha256=None, qualification=False):
    """Publish a fresh explicit attempt through the native authenticated writer.

    No automatic resume: existing receipts must be read back/reconciled first.
    An explicit new attempt may repeat all deterministic events; the native
    consumer deduplicates those identities. Never interpret ACK as DB proof.
    """
    if qualification is not True:
        raise ValueError("release publishing is not qualified; explicit development qualification required")
    profile = audit_profile()
    profile_sha = digest(canonical(profile).encode())
    if profile_sha != expected_profile_sha256:
        raise ValueError("exact qualification broker profile SHA-256 approval required")
    if not profile["brokers"]:
        raise ValueError("qualification Kafka brokers required")
    for broker in profile["brokers"]:
        parsed = urlsplit("//" + broker)
        try:
            port = parsed.port
        except ValueError:
            raise ValueError("invalid qualification broker") from None
        if not _loopback(parsed.hostname) or not port or parsed.username or parsed.password or parsed.path or parsed.query or parsed.fragment:
            raise ValueError("qualification Kafka brokers must be loopback host:port only")
    data, summary = prepare_requests(plan_root, expected_items_sha256, sources, expected_plan_sha256=expected_plan_sha256)
    if not data:
        raise ValueError("selection has no Audit candidates to publish")
    requests = [strict_loads(line) for line in data.splitlines()]
    path = Path(receipt_path)
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    receipt = {"format_version": 1, "stage": "publication_pending_outcome_unknown",
               "plan_sha256": expected_plan_sha256, "profile_sha256": profile_sha,
               "qualification": True, "release_ready": False,
               "items_sha256": expected_items_sha256, "request_sha256": digest(data),
               "summary": summary, "acknowledged_count": 0, "unknown_count": len(requests),
               "database_confirmation": False,
               "entries": [{"event_id": request["payload"]["event_id"],
                            "content_hash": "sha256:" + digest(canonical(request["payload"]).encode()),
                            "status": "outcome_unknown_requires_reconciliation"} for request in requests]}
    _write_receipt(path, receipt, initial=True)
    failed, replies = False, []
    try:
        process = subprocess.run([str(validator), "--publish-audit", "--qualification", "--expected-plan-sha256", digest(data)],
                                 input=data, capture_output=True)
        failed = process.returncode != 0
        replies = [strict_loads(line) for line in process.stdout.splitlines()]
        if len(replies) != len(requests):
            failed = True
            replies = []
    except (OSError, ValueError):
        failed = True
    for ordinal, request in enumerate(requests):
        reply = replies[ordinal] if ordinal < len(replies) else None
        entry = receipt["entries"][ordinal]
        if _confirmed_ack(reply, request):
            entry.update(status="kafka_ack_not_database_confirmation", kafka=reply["kafka"])
            receipt["acknowledged_count"] += 1
            receipt["unknown_count"] -= 1
        else:
            failed = True
    receipt["stage"] = "publication_failed_requires_reconciliation" if failed else "kafka_ack_not_database_confirmation"
    _write_receipt(path, receipt)
    if failed:
        raise ValueError("Audit publication not fully acknowledged; private receipt requires reconciliation")
    return receipt
