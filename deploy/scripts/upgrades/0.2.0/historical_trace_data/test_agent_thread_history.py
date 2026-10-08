# Copyright (c) 2026 OpenBKN
# SPDX-License-Identifier: LicenseRef-OpenBKN
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.
import unittest
from agent_thread_history import plan_agent_thread_history
from test_agent_history import fixture


class AgentThreadHistoryTests(unittest.TestCase):
    def test_messages_derive_reply_calls_separately_without_technical_traces(self):
        plan = plan_agent_thread_history(fixture(), "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["core"]["conversations"]), 1)
        self.assertEqual(len(plan["core"]["interactions"]), 2)
        self.assertEqual(len(plan["core"]["operations"]), 2)
        self.assertEqual(plan["stats"]["derived_reply_operation_count"], 2)
        self.assertEqual(plan["stats"]["original_tool_call_count"], 0)
        for fact, receipt in zip(plan["core"]["call_facts"], plan["core"]["receipts"]):
            self.assertEqual(fact["tool_name"], "bkn.agent.chat")
            self.assertEqual(fact["protocol"], "internal")
            self.assertEqual(fact["status"], "completed")
            self.assertFalse(fact.get("parent_operation_id"))
            self.assertEqual(fact["request_id"], "")
            self.assertEqual(fact["trace_id"], "")
            self.assertEqual(fact["span_id"], "")
            self.assertEqual(receipt["request_id"], "")
            self.assertEqual(receipt["trace_id"], "")
            self.assertEqual(len(receipt["artifact_refs"]), 2)
        self.assertEqual(plan["core"]["call_facts"][0]["output"]["inline"], "500 units")
        events = plan["message_events"]
        self.assertEqual(len(events), 2)
        self.assertEqual(events[0]["event_type"], "agent.interaction.started")
        self.assertEqual(
            events[0]["interaction_id"],
            plan["core"]["interactions"][0]["interaction_id"],
        )
        self.assertEqual(
            events[0]["payload"]["question_artifact_ref"],
            "artifact:" + plan["artifacts"][0]["artifact_id"],
        )
        self.assertEqual(events[0]["payload"]["agent_id"], "agent-1")
        self.assertEqual(events[0]["payload"]["mode"], "chat")
        self.assertEqual(events[0]["started_at"], "2026-09-01T00:00:01.000000Z")
        self.assertFalse(
            any(
                k in events[0]
                for k in ("trace_id", "span_id", "request_id", "operation_id")
            )
        )
        repeated = plan_agent_thread_history(fixture(), "2026-10-01T00:00:00Z")
        self.assertEqual(repeated["message_events"], events)
        self.assertEqual(plan["records"], [])
        self.assertTrue(all(a["trace_id"] == "" for a in plan["artifacts"]))
        self.assertEqual(
            [a["content"] for a in plan["artifacts"]],
            ["Inventory?", "500 units", "BOM?", "3 materials"],
        )

    def test_empty_thread_stays_active_without_invented_failed_round(self):
        plan = plan_agent_thread_history(fixture()[:1], "2026-10-01T00:00:00Z")
        self.assertEqual(plan["core"]["conversations"][0]["status"], "active")
        self.assertEqual(plan["core"]["interactions"], [])
        self.assertEqual(plan["artifacts"], [])

    def test_analysis_tasks_never_become_business_conversations(self):
        records = fixture()[:1] + [
            {
                "kind": "agent_task",
                "row": {
                    "f_task_id": "analysis",
                    "f_agent_id": "business_provenance_claim_attribution",
                    "f_account_id": "user-1",
                    "f_input": '{"message":"INPUT_JSON:{}"}',
                },
            }
        ]
        plan = plan_agent_thread_history(records, "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["source_map"]), 1)
        self.assertEqual(len(plan["core"]["conversations"]), 1)
        self.assertEqual(plan["stats"]["excluded_task_count"], 1)

    def test_exact_host_binding_reuses_core_without_question_matching(self):
        existing = {
            "conversations": [
                {
                    "conversation_id": "real",
                    "external_conversation_key": "thread-1",
                    "owner": {
                        "effective_subject_type": "user",
                        "effective_subject_id": "user-1",
                        "application_principal_id": "client",
                    },
                    "generation": 2,
                }
            ],
            "interactions": [
                {"interaction_id": "i1", "conversation_id": "real", "ordinal": 7}
            ],
        }
        plan = plan_agent_thread_history(
            fixture(), "2026-10-01T00:00:00Z", existing_core=existing
        )
        self.assertEqual(plan["core"]["conversations"], [])
        self.assertEqual(plan["core"]["interactions"], [])
        self.assertEqual(plan["artifacts"], [])
        self.assertEqual(plan["source_map"][0]["conversation_id"], "real")
        self.assertTrue(
            any(i["reason"] == "thread_round_binding_missing" for i in plan["issues"])
        )
        bound = plan_agent_thread_history(
            fixture(),
            "2026-10-01T00:00:00Z",
            existing_core=existing,
            lifecycle_bindings={
                "thread-1": {
                    "conversation_id": "real",
                    "generation": 2,
                    "interaction_ids": {"h1": "i1"},
                }
            },
        )
        self.assertEqual(bound["message_content_recovery"][0]["interaction_id"], "i1")
        self.assertEqual(bound["message_content_recovery"][0]["question"], "Inventory?")

    def test_actual_tool_calls_stay_separate_from_derived_reply_operations(self):
        from agent_history import decode_messages
        import msgpack

        records = fixture()
        blob = records[-1]["row"]
        messages = decode_messages(blob["blob_hex"])
        messages[1:1] = [
            {
                "type": "ai",
                "id": "a-tool",
                "content": "",
                "tool_calls": [
                    {
                        "id": "call-original",
                        "name": "query_inventory",
                        "args": {"material": "M1"},
                    }
                ],
            },
            {
                "type": "tool",
                "id": "tool-original",
                "tool_call_id": "call-original",
                "content": "500",
                "status": "success",
            },
        ]
        blob["blob_hex"] = msgpack.packb(messages, use_bin_type=True).hex()
        plan = plan_agent_thread_history(records, "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["core"]["operations"]), 3)
        self.assertEqual(plan["core"]["operations"][1]["tool_name"], "query_inventory")
        fact = plan["core"]["call_facts"][1]
        self.assertEqual(fact["request_id"], "")
        self.assertEqual(fact["trace_id"], "")
        self.assertEqual(fact.get("span_id", ""), "")
        self.assertFalse(fact.get("parent_operation_id"))
        self.assertEqual(fact["input"]["inline"], {"material": "M1"})
        self.assertEqual(fact["output"]["inline"], "500")
        self.assertEqual(
            fact["interaction_id"], plan["core"]["interactions"][0]["interaction_id"]
        )

    def test_original_execution_context_is_preserved_when_retained(self):
        from agent_history import decode_messages
        import msgpack

        records = fixture()
        blob = records[-1]["row"]
        messages = decode_messages(blob["blob_hex"])
        context = {
            "request_id": "request-original",
            "trace_id": "0123456789abcdef0123456789abcdef",
            "span_id": "0123456789abcdef",
            "operation_id": "op-original",
            "receipt_id": "receipt-original",
            "attempt": 2,
            "protocol": "mcp",
            "parent_operation_id": "parent-original",
        }
        messages[1:1] = [
            {
                "type": "ai",
                "id": "tool-request",
                "content": "",
                "tool_calls": [
                    {"id": "call-source", "name": "query_inventory", "args": {}}
                ],
            },
            {
                "type": "tool",
                "id": "tool-result",
                "tool_call_id": "call-source",
                "content": "500",
                "status": "success",
                "additional_kwargs": {"bkn_context": context},
            },
        ]
        blob["blob_hex"] = msgpack.packb(messages, use_bin_type=True).hex()
        plan = plan_agent_thread_history(records, "2026-10-01T00:00:00Z")
        fact = plan["core"]["call_facts"][1]
        for field, value in context.items():
            self.assertEqual(fact[field], value)
        self.assertEqual(plan["core"]["receipts"][1]["attempt"], 2)
        self.assertEqual(
            plan["core"]["operations"][1]["parent_operation_id"], "parent-original"
        )

    def test_missing_answer_reply_stays_pending_without_failure_or_finished_time(self):
        plan = plan_agent_thread_history(fixture()[:4], "2026-10-01T00:00:00Z")
        fact = plan["core"]["call_facts"][0]
        self.assertEqual(fact["status"], "pending")
        self.assertIsNone(fact["finished_at"])
        self.assertIsNone(fact["output"])
        self.assertIsNone(fact["error"])
        self.assertIsNone(plan["core"]["receipts"][0]["terminal_at"])
        self.assertEqual(len(plan["core"]["receipts"][0]["artifact_refs"]), 1)
        self.assertEqual(plan["stats"]["derived_reply_operation_count"], 1)
        self.assertEqual(plan["stats"]["original_tool_call_count"], 0)

    def test_empty_human_keeps_round_and_reports_question_field_default(self):
        import msgpack

        records = fixture()[:4]
        records[-1]["row"]["blob_hex"] = msgpack.packb(
            [{"type": "human", "id": "h-empty", "content": ""}], use_bin_type=True
        ).hex()
        plan = plan_agent_thread_history(records, "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["core"]["interactions"]), 1)
        self.assertEqual(len(plan["artifacts"]), 1)
        self.assertEqual(
            plan["artifacts"][0]["content"],
            "[Question content not recorded in source]",
        )
        self.assertEqual(
            plan["message_events"][0]["payload"]["question_artifact_ref"],
            "artifact:" + plan["artifacts"][0]["artifact_id"],
        )
        missing = next(
            d
            for d in plan["defaults"]
            if d.get("field") == "question_artifact.content.text"
        )
        self.assertEqual(missing["original_value"], "")
        self.assertEqual(missing["source_message_id"], "h-empty")
        self.assertEqual(plan["core"]["call_facts"][0]["input"]["inline"], "")


class QuestionCaptureContractTests(unittest.TestCase):
    def test_message_question_is_native_text_with_matching_start_hash(self):
        import hashlib
        from snapshot import canonical
        plan = plan_agent_thread_history(fixture(), "2026-10-01T00:00:00Z")
        artifacts = {"artifact:" + a["artifact_id"]: a for a in plan["artifacts"]}
        for event in plan["message_events"]:
            payload = event["payload"]
            artifact = artifacts[payload["question_artifact_ref"]]
            self.assertIsInstance(artifact["content"], str)
            expected = "sha256:" + hashlib.sha256(canonical(artifact["content"]).encode()).hexdigest()
            self.assertEqual(payload["content_hash"], expected)
