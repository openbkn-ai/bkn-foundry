import tempfile
from pathlib import Path
import unittest

from run_upgrade import run, DeploymentRuntime
from test_logs import row


class FakeRuntime:
    def __init__(self):
        self.sent = []
        self.rows = [dict(kind="audit", source_id="vega", row=row()),
                     dict(kind="audit", source_id="vega", row=row(event_id="missing-status", outcome="success", failure_code="")),
                     dict(kind="evidence", source_id="bkn-backend", row={"envelope": "old"})]
        self.existing = False
        self.fail = False
        self.evidence_calls = []
        self.span_calls = []
        self.safe_evidence = False
        self.safe_spans = False

    def discover(self):
        return {"instance": "test-instance", "environment": "test", "target_image": "020-test"}

    def snapshot(self):
        return self.rows, []

    def validate(self, requests):
        from snapshot import canonical, digest
        return [dict(accepted=True, reason="valid", canonical_payload=request["payload"],
                     content_hash="sha256:" + digest(canonical(request["payload"]).encode()),
                     event_id=request["payload"]["event_id"]) for request in requests]

    def fetch(self, item):
        if self.existing:
            occurred = item["payload"]["occurred_at"]
            return dict(content_hash=item["content_hash"], dedup_hash=item["content_hash"], payload=item["payload"],
                        target_table="audit_event_" + occurred[:7].replace("-", ""), source_id=item["source_id"],
                        occurred_at=occurred, topic="openbkn.audit.v1", partition=0, offset=1)
        return None

    def publish(self, requests):
        if self.fail:
            raise ValueError("private error")
        self.sent.extend(requests)
        self.existing = True
        return [{"accepted": True, "event_id": request["payload"]["event_id"],
                 "reason": "kafka_ack_not_database_confirmation"} for request in requests]

    def migrate_evidence(self, records):
        self.evidence_calls.extend(records)
        if not self.safe_evidence:
            return {"verified": 0, "retained": len(records), "reason": "evidence_dependency_set_not_verified"}
        return {"verified": len(records), "retained": 0}

    def migrate_spans(self, records):
        self.span_calls.extend(records)
        if not self.safe_spans:
            return {"verified": 0, "retained": len(records), "reason": "span_target_config_not_verified"}
        return {"verified": len(records), "retained": 0}


class UpgradeRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_single_run_partial_conversion_writes_only_confirmed_log_and_report(self):
        runtime = FakeRuntime()
        result = run(runtime, self.root)
        self.assertEqual(result["source_count"], 3)
        self.assertEqual(result["target_verified_count"], 1)
        self.assertEqual(len(runtime.sent), 1)
        self.assertEqual(result["retained_count"], 2)
        self.assertTrue((Path(result["run_directory"]) / "report.md").exists())

    def test_repeat_reads_before_sending_no_duplicate_publish(self):
        runtime = FakeRuntime()
        run(runtime, self.root)
        second = run(runtime, self.root)
        self.assertEqual(len(runtime.sent), 1)
        self.assertEqual(second["already_verified_count"], 1)

    def test_publish_failure_still_returns_markdown_report_and_preserves_source(self):
        runtime = FakeRuntime()
        runtime.fail = True
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        text = (Path(result["run_directory"]) / "report.md").read_text()
        self.assertIn("publication_requires_readback", text)
        self.assertNotIn("private error", text)
        self.assertTrue((self.root / "test-instance" / "source" / "records.jsonl").exists())

    def test_precheck_failure_still_generates_report(self):
        runtime = FakeRuntime()
        runtime.discover = lambda: (_ for _ in ()).throw(ValueError("private configuration"))
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        self.assertTrue((Path(result["run_directory"]) / "report.md").exists())

    def test_safe_evidence_and_span_are_delegated_to_existing_writers(self):
        runtime = FakeRuntime()
        runtime.safe_evidence = runtime.safe_spans = True
        runtime.rows = [dict(kind="evidence", source_id="bkn-backend", row={"event_id": "e", "envelope": {"event": {}}}, frozen_015_provenance=True),
                        dict(kind="span", source_id="agent-observability", row={"index": "old", "id": "s", "_source": {}}, frozen_015_provenance=True)]
        result = run(runtime, self.root)
        self.assertEqual(len(runtime.evidence_calls), 1)
        self.assertEqual(len(runtime.span_calls), 1)
        self.assertEqual(result["target_verified_count"], 2)

    def test_unknown_evidence_dependency_is_retained_without_writer_call(self):
        runtime = FakeRuntime()
        runtime.rows = [dict(kind="evidence", source_id="bkn-backend", row={"event_id": "e", "envelope": {"event": {}}})]
        result = run(runtime, self.root)
        self.assertEqual(runtime.evidence_calls, [])
        self.assertEqual(result["retained_count"], 1)


if __name__ == "__main__":
    unittest.main()
