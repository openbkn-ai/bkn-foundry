import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

from run_upgrade import run, DeploymentRuntime
from snapshot import canonical, digest, strict_loads
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
        self.log_events = []
        self.safe_evidence = False
        self.safe_spans = False
        self.cluster_uid = "cluster-one"
        self.context = "default"
        self.snapshot_calls = 0

    def discover(self):
        return {"instance": "instance-" + digest(self.cluster_uid.encode()),
                "cluster_uid": self.cluster_uid, "context": self.context,
                "environment": "test", "target_image": "020-test"}

    def snapshot(self):
        self.snapshot_calls += 1
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
            return {"verified": 0, "retained": len(records), "reason": "missing_native_trace_dependencies"}
        return {"verified": len(records), "retained": 0}

    def migrate_spans(self, records):
        self.span_calls.extend(records)
        if not self.safe_spans:
            return {"verified": 0, "retained": len(records), "reason": "span_target_config_not_verified"}
        return {"verified": len(records), "retained": 0}

    def publish_logs(self, events):
        self.log_events.extend(events)
        return {"created": len(events), "already_verified": 0, "conflict": 0}


class UpgradeRunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def test_unconverted_record_cannot_report_completion(self):
        runtime = FakeRuntime()
        runtime.rows = [dict(kind="audit", source_id="vega", row=row(action="read"))]
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        self.assertEqual(result["state"], "partial_requires_reconciliation")
        self.assertEqual(result["retained_count"], 1)
        self.assertEqual(runtime.sent, [])

    def test_single_run_partial_conversion_writes_only_confirmed_log_and_report(self):
        runtime = FakeRuntime()
        result = run(runtime, self.root)
        self.assertEqual(result["source_count"], 3)
        self.assertEqual(result["target_verified_count"], 2)
        self.assertEqual(len(runtime.sent), 2)
        self.assertEqual(len(runtime.log_events), 0)
        self.assertEqual(result["retained_count"], 1)
        self.assertTrue((Path(result["run_directory"]) / "report.md").exists())

    def test_repeat_reads_before_sending_no_duplicate_publish(self):
        runtime = FakeRuntime()
        run(runtime, self.root)
        second = run(runtime, self.root)
        self.assertEqual(len(runtime.sent), 2)
        self.assertEqual(second["already_verified_count"], 2)

    def test_publish_failure_still_returns_markdown_report_and_preserves_source(self):
        runtime = FakeRuntime()
        runtime.fail = True
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        text = (Path(result["run_directory"]) / "report.md").read_text()
        self.assertIn("publication_requires_readback", text)
        self.assertNotIn("private error", text)
        self.assertTrue((self.root / runtime.discover()["instance"] / "source" / "records.jsonl").exists())

    def test_rebuilt_cluster_with_same_context_gets_its_own_snapshot(self):
        first = FakeRuntime()
        run(first, self.root)
        second = FakeRuntime()
        second.cluster_uid = "cluster-two"
        second.rows = [dict(kind="audit", source_id="vega", row=row(event_id="second-cluster"))]
        result = run(second, self.root)
        self.assertEqual(second.snapshot_calls, 1)
        self.assertEqual(result["source_count"], 1)
        self.assertEqual(second.sent[0]["payload"]["event_id"], second.validate(second.sent)[0]["event_id"])
        self.assertNotEqual(first.sent[0]["payload"]["event_id"], second.sent[0]["payload"]["event_id"])

    def test_snapshot_identity_mismatch_stops_before_publish(self):
        runtime = FakeRuntime()
        run(runtime, self.root)
        manifest_path = self.root / runtime.discover()["instance"] / "source" / "snapshot.json"
        manifest = strict_loads(manifest_path.read_text())
        manifest["metadata"]["deployment"]["cluster_uid"] = "another-cluster"
        manifest_path.write_text(canonical(manifest))
        runtime.sent.clear()
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        self.assertEqual(runtime.sent, [])
        self.assertIn("snapshot_deployment_mismatch", (Path(result["run_directory"]) / "report.md").read_text())

    def test_context_rename_on_same_cluster_reuses_snapshot(self):
        runtime = FakeRuntime()
        run(runtime, self.root)
        runtime.context = "renamed"
        result = run(runtime, self.root)
        self.assertFalse(result["complete"])
        self.assertEqual(runtime.snapshot_calls, 1)

    def test_discovery_uses_cluster_uid_not_context_label(self):
        pods = {"items": [
            {"metadata": {"namespace": "resource", "name": "mariadb-0"}, "status": {"phase": "Running"}},
            {"metadata": {"namespace": "openbkn", "name": "agent-observability-0"},
             "status": {"phase": "Running"}, "spec": {"containers": [
                 {"name": "agent-observability", "image": "020", "env": [{"name": "BKN_AUDIT_ENVIRONMENT", "value": "test"}, {"name": "OPENSEARCH_ENDPOINT", "value": "http://opensearch"}, {"name": "OPENSEARCH_LOG_INDEX", "value": "logs"}, {"name": "OPENSEARCH_EVIDENCE_INDEX", "value": "evidence"}]}]}}]}
        for uid in ("cluster-one", "cluster-two"):
            with patch("run_upgrade._command", side_effect=[b"default", canonical({"metadata": {"uid": uid}}).encode(), canonical(pods).encode(), b""]):
                deployment = DeploymentRuntime().discover()
                self.assertEqual(deployment["cluster_uid"], uid)
                self.assertEqual(deployment["instance"], "instance-" + digest(uid.encode()))

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

class OpenSearchConnectionTests(unittest.TestCase):
    def runtime(self, endpoint):
        runtime = DeploymentRuntime()
        runtime.context = "customer-cluster"
        runtime.opensearch_endpoint = endpoint
        return runtime

    def test_customer_service_namespace_port_and_noisy_output(self):
        import subprocess
        import sys
        original_popen = subprocess.Popen
        processes = []
        def launch(command, **kwargs):
            self.assertEqual(command, ["kubectl", "--context", "customer-cluster", "-n", "customer-data", "port-forward", "svc/customer-search", "0:9443"])
            self.assertNotEqual(kwargs["stdout"], subprocess.PIPE)
            process = original_popen([sys.executable, "-c", "import sys; print('startup notice'); print('Forwarding from 127.0.0.1:51234 -> 9443', flush=True); sys.stdout.write('Handling connection\\n' * 10000); sys.stdout.flush()"], **kwargs)
            processes.append(process)
            return process
        with patch("run_upgrade.subprocess.Popen", side_effect=launch):
            with self.runtime("https://customer-search.customer-data.svc.cluster.local:9443/prefix")._opensearch_connection() as (endpoint, forwarded):
                self.assertEqual(endpoint, "https://127.0.0.1:51234/prefix")
                self.assertTrue(forwarded)
                processes[0].wait(timeout=5)
        self.assertIsNotNone(processes[0].poll())

    def test_startup_timeout_cleans_up_process(self):
        import subprocess
        import sys
        original_popen = subprocess.Popen
        processes = []
        def launch(_, **kwargs):
            process = original_popen([sys.executable, "-c", "import time; time.sleep(30)"], **kwargs)
            processes.append(process)
            return process
        with patch("run_upgrade.subprocess.Popen", side_effect=launch):
            with self.assertRaisesRegex(RuntimeError, "opensearch_port_forward_failed"):
                with self.runtime("http://search.data.svc.cluster.local:9201")._opensearch_connection(timeout=0.01):
                    self.fail("unready forward yielded")
        self.assertIsNotNone(processes[0].poll())

    def test_external_endpoint_is_used_directly(self):
        with patch("run_upgrade.subprocess.Popen") as launch:
            with self.runtime("https://customer.example:9443")._opensearch_connection() as value:
                self.assertEqual(value, ("https://customer.example:9443", False))
            launch.assert_not_called()

class NativeWriterAccountingTests(unittest.TestCase):
    def test_mixed_writer_results_retain_exact_rows_without_double_count(self):
        with tempfile.TemporaryDirectory() as root:
            runtime = FakeRuntime()
            runtime.rows = [dict(kind='evidence', source_id='backend', row={'event_id': str(i), 'envelope': {}},
                                 frozen_015_provenance=True) for i in range(3)]
            runtime.migrate_evidence = lambda _: {'results': [
                {'verified': True, 'target_id': 'aggregate-first'},
                {'verified': False, 'reason': 'multiple_request_contexts_for_trace'},
                {'verified': True, 'target_id': 'aggregate-last', 'losses': ['missing_core_receipt']}]}
            result = run(runtime, root)
            self.assertEqual(result['target_verified_count'], 2)
            self.assertEqual(result['retained_count'], 1)
            self.assertFalse(result['complete'])
            self.assertEqual(result['state'], 'partial_requires_reconciliation')
            final = [strict_loads(line) for line in (Path(result['run_directory']) / 'final-items.jsonl').read_text().splitlines()]
            self.assertEqual([item['disposition'] for item in final], ['writer_verified', 'archive', 'writer_verified'])

    def test_native_writer_failure_is_not_reported_as_successful_loss(self):
        with tempfile.TemporaryDirectory() as root:
            runtime = FakeRuntime()
            runtime.rows = [dict(kind='evidence', source_id='backend', row={'envelope': {}}, frozen_015_provenance=True)]
            runtime.migrate_evidence = lambda _: (_ for _ in ()).throw(RuntimeError('write failed'))
            result = run(runtime, root)
            self.assertFalse(result['complete'])
            self.assertEqual(result['retained_count'], 1)

    def test_audit_final_record_is_verified_after_native_readback(self):
        with tempfile.TemporaryDirectory() as root:
            runtime = FakeRuntime()
            runtime.rows = [dict(kind='audit', source_id='vega', row=row())]
            result = run(runtime, root)
            item = strict_loads((Path(result['run_directory']) / 'final-items.jsonl').read_text())
            self.assertEqual(item['disposition'], 'writer_verified')
            self.assertEqual(item['target_status'], 'verified')
            self.assertEqual(item['state'], 'created')
            second = run(runtime, root)
            again = strict_loads((Path(second['run_directory']) / 'final-items.jsonl').read_text())
            self.assertEqual(again['state'], 'already_verified')

    def test_audit_failed_readback_is_explicit_per_record(self):
        with tempfile.TemporaryDirectory() as root:
            runtime = FakeRuntime()
            runtime.rows = [dict(kind='audit', source_id='vega', row=row())]
            runtime.fail = True
            result = run(runtime, root)
            item = strict_loads((Path(result['run_directory']) / 'final-items.jsonl').read_text())
            self.assertEqual(item['disposition'], 'requires_reconciliation')
            self.assertEqual(item['target_status'], 'missing')
            self.assertEqual(item['reason'], 'publication_requires_readback')


class AuditPublicationRecoveryTests(unittest.TestCase):
    def test_unknown_publication_outcome_forces_fresh_database_readback(self):
        runtime = object.__new__(DeploymentRuntime)
        runtime.source = object()
        runtime._audit_ids = ["event-one"]
        runtime._audit_cache = {}
        runtime._audit_published = False
        runtime._native = lambda *_: (_ for _ in ()).throw(ValueError("ack lost"))
        with self.assertRaises(ValueError):
            runtime.publish([])
        with patch("run_upgrade.reconcile.fetch_audits", return_value={"event-one": {"persisted": True}}) as fetch:
            self.assertEqual(runtime.fetch({"target_id": "event-one"}), {"persisted": True})
            fetch.assert_called_once_with(runtime.source, ["event-one"])
