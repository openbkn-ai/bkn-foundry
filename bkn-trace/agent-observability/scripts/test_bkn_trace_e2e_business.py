# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
# Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

import unittest
from argparse import Namespace
from unittest.mock import patch

import bkn_trace_e2e_business as e2e


class BusinessE2EContractTest(unittest.TestCase):
    def test_poll_core_waits_until_business_payload_is_ready(self):
        args = Namespace(retries=2, retry_delay=0)
        responses = [
            (200, {"status": "running"}),
            (200, {"status": "completed", "result_preview": "业务结论"}),
        ]

        with patch.object(e2e, "_core_get", side_effect=responses):
            result = e2e._poll_core(
                args,
                "/requests/req-1",
                ready=lambda body: (
                    body.get("status") == "completed"
                    and bool(body.get("result_preview"))
                ),
            )

        self.assertEqual(result["status"], "completed")

    def test_extract_run_identity_uses_real_response_headers(self):
        identity = e2e.extract_run_identity(
            {
                "Traceparent": (
                    "00-ce31e2d297bd4ee0b1313ca3bdcd1acf-"
                    "abcdef1234567890-01"
                ),
                "Bkn-Request-Id": "req_supply_chain_001",
            }
        )

        self.assertEqual(identity.trace_id, "ce31e2d297bd4ee0b1313ca3bdcd1acf")
        self.assertEqual(identity.request_id, "req_supply_chain_001")

    def test_evaluate_requires_business_content_and_real_data_evidence(self):
        report = e2e.evaluate_business_e2e(
            agent_response={"status": "succeeded", "output": "采购订单存在逾期风险"},
            identity=e2e.RunIdentity(
                trace_id="ce31e2d297bd4ee0b1313ca3bdcd1acf",
                request_id="req_supply_chain_001",
            ),
            request_summary={
                "request_id": "req_supply_chain_001",
                "status": "completed",
                "question_preview": "哪些采购订单存在逾期？",
                "result_preview": "订单 PO-001 已逾期。",
            },
            trace_page={
                "entries": [
                    {
                        "trace_id": "ce31e2d297bd4ee0b1313ca3bdcd1acf",
                        "request_id": "req_supply_chain_001",
                    }
                ]
            },
            trace_graph={
                "trace_id": "ce31e2d297bd4ee0b1313ca3bdcd1acf",
                "status": "ok",
                "page": {"node_count": 3, "edge_count": 2},
                "data": {
                    "nodes": [
                        {"span_id": "span-root"},
                        {"span_id": "span-agent", "parent_span_id": "span-root"},
                        {"span_id": "span-data", "parent_span_id": "span-agent"},
                    ],
                    "edges": [
                        {"parent_span_id": "span-root", "child_span_id": "span-agent"},
                        {"parent_span_id": "span-agent", "child_span_id": "span-data"},
                    ],
                },
            },
            evidence_chain={
                "data": {
                    "claims": [
                        {
                            "claim_id": "claim-1",
                            "source_event_ids": ["evt-query"],
                        }
                    ],
                    "artifact_links": [
                        {"artifact_ref": "artifact:question-1", "artifact_type": "question"},
                        {"artifact_ref": "artifact:result-1", "artifact_type": "result"},
                        {"artifact_ref": "artifact:query-1", "artifact_type": "query"},
                        {"artifact_ref": "artifact:data-1", "artifact_type": "data_result"},
                    ],
                }
            },
            business_graph={
                "data": {
                    "nodes": [
                        {"node_type": "claim"},
                        {"node_type": "object"},
                        {
                            "node_type": "operation",
                            "label": "data.query.observed",
                            "properties": {"producer_module": "vega-data"},
                        },
                        {
                            "node_type": "operation",
                            "label": "retrieval.completed",
                            "properties": {"producer_module": "context-loader"},
                        },
                        {
                            "node_type": "operation",
                            "label": "model.call.observed",
                            "properties": {"producer_module": "mf-model-api"},
                        },
                    ],
                    "edges": [{"edge_type": "supports"}],
                }
            },
            artifacts=[
                {"artifact_type": "question", "content": {"text": "哪些采购订单存在逾期？"}},
                {"artifact_type": "result", "content": {"text": "订单 PO-001 已逾期。"}},
                {"artifact_type": "query", "content": {"conditions": [{"field": "deliverdate"}]}},
                {"artifact_type": "data_result", "content": {"rows": [{"order": "PO-001"}]}},
            ],
        )

        self.assertTrue(report.passed, report.to_dict())
        self.assertEqual(report.trace_id, "ce31e2d297bd4ee0b1313ca3bdcd1acf")
        self.assertEqual(report.request_id, "req_supply_chain_001")

    def test_evaluate_rejects_hash_only_or_missing_data_artifacts(self):
        report = e2e.evaluate_business_e2e(
            agent_response={"status": "succeeded", "output": "结论"},
            identity=e2e.RunIdentity(
                trace_id="ce31e2d297bd4ee0b1313ca3bdcd1acf",
                request_id="req_supply_chain_002",
            ),
            request_summary={
                "request_id": "req_supply_chain_002",
                "status": "completed",
                "question_preview": "采购风险？",
                "result_preview": "存在风险。",
            },
            trace_page={"entries": [{"trace_id": "ce31e2d297bd4ee0b1313ca3bdcd1acf"}]},
            trace_graph={
                "trace_id": "different-trace-id",
                "status": "error",
                "page": {"node_count": 0, "edge_count": 0},
                "data": {"nodes": [], "edges": []},
            },
            evidence_chain={
                "data": {
                    "claims": [{"claim_id": "claim-1", "source_event_ids": ["evt-1"]}],
                    "artifact_links": [],
                }
            },
            business_graph={
                "data": {
                    "nodes": [
                        {"node_type": "claim"},
                        {"node_type": "object"},
                        {
                            "node_type": "operation",
                            "label": "retrieval.completed",
                            "properties": {"producer_module": "context-loader"},
                        },
                    ],
                    "edges": [{"edge_type": "supports"}],
                }
            },
            artifacts=[
                {"artifact_type": "question", "content_hash": "sha256:question"},
                {"artifact_type": "result", "content_hash": "sha256:result"},
            ],
        )

        self.assertFalse(report.passed)
        failed = {check.name for check in report.checks if check.status == "fail"}
        self.assertIn("artifact.question.content", failed)
        self.assertIn("artifact.result.content", failed)
        self.assertIn("artifact.query.content", failed)
        self.assertIn("artifact.data-result.content", failed)
        self.assertIn("graph.real_module_count", failed)
        self.assertIn("graph.data_query", failed)
        self.assertIn("trace-graph.identity", failed)
        self.assertIn("trace-graph.spans", failed)
        self.assertIn("trace-graph.edges", failed)


if __name__ == "__main__":
    unittest.main()
