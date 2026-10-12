import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from apply_audit import apply as raw_apply, prepare_requests as raw_prepare, audit_profile
from snapshot import canonical, digest


def prepare_requests(root, checksum, sources):
    return raw_prepare(root, checksum, sources, expected_plan_sha256=digest((Path(root) / "plan.json").read_bytes()))


def apply(root, checksum, sources, validator, path):
    return raw_apply(root, checksum, sources, validator, path,
                     expected_plan_sha256=digest((Path(root) / "plan.json").read_bytes()),
                     expected_profile_sha256=digest(canonical(audit_profile()).encode()), qualification=True)


def item(source="vega", disposition="convert", kind="audit", event_id="uuid-1"):
    source_row = {"source_id": source, "kind": kind, "row": {"id": "old-1"}}
    payload = {"source_id": source, "event_id": event_id, "http_status": 400}
    checksum = digest(canonical(payload).encode())
    result = {"source_id": source, "kind": kind, "source": source_row,
              "source_sha256": digest(canonical(source_row).encode()),
              "disposition": disposition, "reason": "fixture"}
    if disposition == "convert":
        result.update(payload=payload, payload_sha256=checksum,
                      content_hash="sha256:" + checksum, target_id=event_id)
    return result


def save(root, items):
    data = "".join(canonical(value) + "\n" for value in items).encode()
    checksum = digest(data)
    counts = {}
    for value in items:
        counts[value["disposition"]] = counts.get(value["disposition"], 0) + 1
    manifest = {"format_version": 1, "items_sha256": checksum, "record_count": len(items),
                "counts": counts, "validation_time": "2026-10-06T00:00:00Z"}
    (root / "items.jsonl").write_bytes(data)
    (root / "plan.json").write_text(canonical(manifest))
    return checksum


def ack(value):
    return {"accepted": True, "reason": "kafka_ack_not_database_confirmation",
            "event_id": value["target_id"], "content_hash": value["content_hash"],
            "kafka": {"topic": "openbkn.audit.v1", "partition": 0, "offset": 42}}


class ApplyAuditTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)
        environment = patch.dict(os.environ, {"BKN_HISTORY_KAFKA_BROKERS": "127.0.0.1:19092", "BKN_HISTORY_KAFKA_MECHANISM": "PLAIN"})
        environment.start()
        self.addCleanup(environment.stop)

    def test_exact_selected_audit_requests_ignore_other_writers(self):
        values = [item(), item(disposition="archive"), item(source="bkn-backend", disposition="blocked"),
                  item(kind="evidence"), item(source="collector", kind="span")]
        checksum = save(self.root, values)
        data, summary = prepare_requests(self.root, checksum, ["vega"])
        requests = [json.loads(line) for line in data.splitlines()]
        self.assertEqual(len(requests), 1)
        self.assertEqual(requests[0], {"kind": "audit", "payload": values[0]["payload"],
                                       "broker_time": "2026-10-06T00:00:00Z"})
        self.assertEqual(summary["publish_count"], 1)
        self.assertEqual(summary["archive_count"], 1)

    def test_selection_is_mandatory_no_default_all(self):
        checksum = save(self.root, [item()])
        for sources in (None, [], "vega", ["collector"]):
            with self.assertRaises(ValueError):
                prepare_requests(self.root, checksum, sources)

    def test_approval_and_manifest_hash_must_match(self):
        checksum = save(self.root, [item()])
        with self.assertRaises(ValueError):
            prepare_requests(self.root, "0" * 64, ["vega"])
        (self.root / "items.jsonl").write_text("changed")
        with self.assertRaises(ValueError):
            prepare_requests(self.root, checksum, ["vega"])

    def test_selected_blocked_row_refuses_whole_audit_publish(self):
        checksum = save(self.root, [item(), item(disposition="blocked")])
        with self.assertRaisesRegex(ValueError, "blocked"):
            prepare_requests(self.root, checksum, ["vega"])

    def test_rechecks_payload_source_and_source_raw_hash(self):
        for field, bad in (("payload_sha256", "bad"), ("content_hash", "bad"),
                           ("source_sha256", "bad"), ("target_id", "other")):
            value = item()
            value[field] = bad
            checksum = save(self.root, [value])
            with self.subTest(field=field), self.assertRaises(ValueError):
                prepare_requests(self.root, checksum, ["vega"])
        value = item()
        value["payload"]["source_id"] = "bkn-backend"
        value["payload_sha256"] = digest(canonical(value["payload"]).encode())
        value["content_hash"] = "sha256:" + value["payload_sha256"]
        checksum = save(self.root, [value])
        with self.assertRaises(ValueError):
            prepare_requests(self.root, checksum, ["vega"])

    def test_same_uuid_different_content_refuses_publish(self):
        first, second = item(), item()
        second["payload"]["http_status"] = 403
        second["payload_sha256"] = digest(canonical(second["payload"]).encode())
        second["content_hash"] = "sha256:" + second["payload_sha256"]
        checksum = save(self.root, [first, second])
        with self.assertRaisesRegex(ValueError, "conflict"):
            prepare_requests(self.root, checksum, ["vega"])

    def test_apply_exact_bytes_native_process_and_private_receipt(self):
        value = item()
        checksum = save(self.root, [value])
        expected, _ = prepare_requests(self.root, checksum, ["vega"])
        reply = (canonical(ack(value)) + "\n").encode()
        with patch("apply_audit.subprocess.run", return_value=subprocess.CompletedProcess([], 0, reply, b"")) as runner:
            receipt = apply(self.root, checksum, ["vega"], "/native-validator", self.root / "receipt.json")
        self.assertEqual(runner.call_args.args[0], ["/native-validator", "--publish-audit", "--qualification", "--expected-plan-sha256", digest(expected)])
        self.assertEqual(runner.call_args.kwargs["input"], expected)
        self.assertNotIn("env", runner.call_args.kwargs)
        self.assertEqual(receipt["stage"], "kafka_ack_not_database_confirmation")
        self.assertEqual(receipt["acknowledged_count"], 1)
        self.assertEqual((self.root / "receipt.json").stat().st_mode & 0o777, 0o600)

    def test_existing_receipt_never_triggers_automatic_resend(self):
        checksum = save(self.root, [item()])
        path = self.root / "receipt.json"
        path.write_text("old")
        with patch("apply_audit.subprocess.run") as runner, self.assertRaises(ValueError):
            apply(self.root, checksum, ["vega"], "/native-validator", path)
        runner.assert_not_called()

    def test_protocol_count_identity_and_ack_errors_emit_failure_receipt(self):
        value = item()
        checksum = save(self.root, [value])
        invalid_replies = [[], [{**ack(value), "event_id": "other"}],
                           [{**ack(value), "accepted": False}], [{**ack(value), "kafka": None}]]
        for ordinal, replies in enumerate(invalid_replies):
            path = self.root / ("failure-%d.json" % ordinal)
            data = "".join(canonical(reply) + "\n" for reply in replies).encode()
            with patch("apply_audit.subprocess.run", return_value=subprocess.CompletedProcess([], 0, data, b"")), self.assertRaises(ValueError):
                apply(self.root, checksum, ["vega"], "/native-validator", path)
            receipt = json.loads(path.read_text())
            self.assertEqual(receipt["stage"], "publication_failed_requires_reconciliation")
            self.assertEqual(receipt["acknowledged_count"], 0)

    def test_transport_failure_retains_unknown_outcome_receipt(self):
        checksum = save(self.root, [item()])
        path = self.root / "transport.json"
        with patch("apply_audit.subprocess.run", side_effect=OSError("private transport error")), self.assertRaises(ValueError):
            apply(self.root, checksum, ["vega"], "/native-validator", path)
        receipt = json.loads(path.read_text())
        self.assertEqual(receipt["unknown_count"], 1)
        self.assertNotIn("private transport error", path.read_text())

    def test_full_manifest_approval_is_mandatory_and_tamper_detected(self):
        checksum = save(self.root, [item()])
        approved = digest((self.root / "plan.json").read_bytes())
        with self.assertRaises(ValueError):
            raw_prepare(self.root, checksum, ["vega"])
        manifest = json.loads((self.root / "plan.json").read_text())
        manifest["validation_time"] = "2025-01-01T00:00:00Z"
        (self.root / "plan.json").write_text(canonical(manifest))
        with self.assertRaises(ValueError):
            raw_prepare(self.root, checksum, ["vega"], expected_plan_sha256=approved)

    def test_release_nonloopback_and_unapproved_profile_refuse_publish(self):
        checksum = save(self.root, [item()])
        plan_sha = digest((self.root / "plan.json").read_bytes())
        profile_sha = digest(canonical(audit_profile()).encode())
        with patch("apply_audit.subprocess.run") as runner:
            with self.assertRaises(ValueError):
                raw_apply(self.root, checksum, ["vega"], "/native", self.root / "release.json", expected_plan_sha256=plan_sha, expected_profile_sha256=profile_sha)
            with self.assertRaises(ValueError):
                raw_apply(self.root, checksum, ["vega"], "/native", self.root / "profile.json", expected_plan_sha256=plan_sha, expected_profile_sha256="0" * 64, qualification=True)
            with patch.dict(os.environ, {"BKN_HISTORY_KAFKA_BROKERS": "production.example:9092"}):
                with self.assertRaises(ValueError):
                    apply(self.root, checksum, ["vega"], "/native", self.root / "production.json")
            runner.assert_not_called()


if __name__ == "__main__":
    unittest.main()
