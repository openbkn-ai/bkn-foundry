import unittest
import msgpack
from agent_history import plan_agent_history, decode_messages


def message(role, content, mid):
    name = {"human": "HumanMessage", "ai": "AIMessage"}[role]
    return msgpack.ExtType(
        5,
        msgpack.packb(
            [
                "langchain_core.messages." + role,
                name,
                {"type": role, "content": content, "id": mid},
                None,
            ],
            use_bin_type=True,
        ),
    )


def fixture():
    rows = [
        {
            "kind": "agent_thread",
            "row": {
                "f_thread_id": "thread-1",
                "f_agent_id": "agent-1",
                "f_account_id": "user-1",
                "f_create_time": 1788220800000,
                "f_update_time": 1788220810000,
            },
        },
        {
            "kind": "agent_definition",
            "row": {"f_agent_id": "agent-1", "f_name": "Supply Agent"},
        },
    ]
    histories = [
        [message("human", "Inventory?", "h1")],
        [message("human", "Inventory?", "h1"), message("ai", "500 units", "a1")],
        [
            message("human", "Inventory?", "h1"),
            message("ai", "500 units", "a1"),
            message("human", "BOM?", "h2"),
        ],
        [
            message("human", "Inventory?", "h1"),
            message("ai", "500 units", "a1"),
            message("human", "BOM?", "h2"),
            message("ai", "3 materials", "a2"),
        ],
    ]
    for index, (stamp, messages) in enumerate(zip([1, 4, 5, 10], histories), 1):
        rows.append(
            {
                "kind": "agent_checkpoint",
                "row": {
                    "thread_id": "thread-1",
                    "checkpoint_id": "cp" + str(index),
                    "checkpoint_ns": "",
                    "checkpoint": {
                        "ts": "2026-09-01T00:00:" + str(stamp).zfill(2) + "Z",
                        "channel_versions": {"messages": "v" + str(index)},
                    },
                },
            }
        )
        rows.append(
            {
                "kind": "agent_blob",
                "row": {
                    "thread_id": "thread-1",
                    "checkpoint_ns": "",
                    "channel": "messages",
                    "version": "v" + str(index),
                    "type": "msgpack",
                    "blob_hex": msgpack.packb(messages, use_bin_type=True).hex(),
                },
            }
        )
    return rows


class AgentHistoryTests(unittest.TestCase):
    def test_original_questions_answers_rounds_and_observed_duration(self):
        plan = plan_agent_history(fixture(), "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["core"]["conversations"]), 1)
        self.assertEqual(plan["core"]["conversations"][0]["agent_name"], "Supply Agent")
        self.assertEqual([i["ordinal"] for i in plan["core"]["interactions"]], [1, 2])
        self.assertEqual(
            [a["content"]["text"] for a in plan["artifacts"]],
            ["Inventory?", "500 units", "BOM?", "3 materials"],
        )
        self.assertTrue(all(a.get("operation_id", "") == "" for a in plan["artifacts"]))
        self.assertEqual(
            plan["core"]["interactions"][0]["created_at"], "2026-09-01T00:00:01.000000Z"
        )
        self.assertEqual(
            plan["core"]["interactions"][0]["terminal_at"],
            "2026-09-01T00:00:04.000000Z",
        )

    def test_task_preserves_structured_result_and_times(self):
        rows = [
            {
                "kind": "agent_task",
                "row": {
                    "f_task_id": "task-1",
                    "f_agent_id": "agent-1",
                    "f_account_id": "user-1",
                    "f_status": "succeeded",
                    "f_input": '{"message":"Query inventory"}',
                    "f_output": '{"count":500}',
                    "f_create_time": 1788220800000,
                    "f_update_time": 1788220803500,
                },
            }
        ]
        plan = plan_agent_history(rows, "2026-10-01T00:00:00Z")
        self.assertEqual(plan["artifacts"][0]["content"]["text"], "Query inventory")
        self.assertIn("500", plan["artifacts"][1]["content"]["text"])
        self.assertEqual(
            plan["core"]["call_facts"][0]["output"]["inline"], {"count": 500}
        )
        self.assertEqual(
            plan["core"]["interactions"][0]["terminal_at"],
            "2026-09-01T00:00:03.500000Z",
        )

    def test_deterministic_repeat_and_cutoff(self):
        rows = fixture()
        rows[-2]["row"]["checkpoint"]["ts"] = "2026-10-02T00:00:00Z"
        first = plan_agent_history(rows, "2026-10-01T00:00:00Z")
        self.assertEqual(
            first, plan_agent_history(list(reversed(rows)), "2026-10-01T00:00:00Z")
        )
        self.assertEqual(len(first["core"]["interactions"]), 2)
        self.assertEqual(len(first["artifacts"]), 3)

    def test_missing_messages_keeps_thread_and_reports_defaults(self):
        plan = plan_agent_history(fixture()[:1], "2026-10-01T00:00:00Z")
        self.assertEqual(len(plan["core"]["conversations"]), 1)
        self.assertEqual(len(plan["core"]["interactions"]), 1)
        self.assertEqual(plan["artifacts"], [])
        self.assertTrue(plan["defaults"])

    def test_unknown_extension_rejected_without_execution(self):
        with self.assertRaises(ValueError):
            decode_messages(msgpack.packb([msgpack.ExtType(99, b"unsafe")]).hex())

    def test_plain_failure_preserved_and_latest_message_revision_used(self):
        rows = fixture()
        messages = [
            message("human", "Inventory?", "h1"),
            message("ai", "Revised answer", "a1"),
            message("human", "BOM?", "h2"),
            message("ai", "3 materials", "a2"),
        ]
        rows[-1]["row"]["blob_hex"] = msgpack.packb(messages, use_bin_type=True).hex()
        rows.append(
            {
                "kind": "agent_task",
                "row": {
                    "f_task_id": "failed-task",
                    "f_agent_id": "agent-1",
                    "f_account_id": "user-1",
                    "f_status": "failed",
                    "f_input": "plain question",
                    "f_output": None,
                    "f_failure_detail": "Upstream unavailable",
                    "f_create_time": 1788220800000,
                    "f_update_time": 1788220803500,
                },
            }
        )
        plan = plan_agent_history(rows, "2026-10-01T00:00:00Z")
        self.assertIn(
            "Revised answer", [a["content"]["text"] for a in plan["artifacts"]]
        )
        self.assertEqual(
            plan["core"]["call_facts"][-1]["error"]["inline"], "Upstream unavailable"
        )


class AgentToolHistoryTests(unittest.TestCase):
    def test_checkpoint_tool_call_and_result_become_native_child_operation(self):
        rows = fixture()
        final_blob = next(r["row"] for r in reversed(rows) if r["kind"] == "agent_blob")
        messages = decode_messages(final_blob["blob_hex"])
        messages[1:1] = [
            {
                "type": "ai",
                "id": "tool-request",
                "content": "",
                "tool_calls": [
                    {
                        "id": "call-1",
                        "name": "query_inventory",
                        "args": {"material": "M1"},
                    }
                ],
            },
            {
                "type": "tool",
                "id": "tool-result",
                "tool_call_id": "call-1",
                "content": '{"inventory":500}',
                "status": "success",
            },
        ]
        final_blob["blob_hex"] = msgpack.packb(messages, use_bin_type=True).hex()
        plan = plan_agent_history(rows, "2026-10-01T00:00:00Z")
        facts = plan["core"]["call_facts"]
        child = next(f for f in facts if f["tool_name"] == "query_inventory")
        parent = next(
            f for f in facts if f["operation_id"] == child["parent_operation_id"]
        )
        first_round = next(i for i in plan["core"]["interactions"] if i["ordinal"] == 1)
        self.assertEqual(child["interaction_id"], first_round["interaction_id"])
        self.assertEqual(child["interaction_id"], parent["interaction_id"])
        self.assertEqual(child["trace_id"], parent["trace_id"])
        self.assertEqual(child["input"]["inline"], {"material": "M1"})
        self.assertEqual(child["output"]["inline"], '{"inventory":500}')
        self.assertEqual(len(plan["core"]["interactions"]), 2)
        self.assertEqual(len(facts), 3)


class AgentCutoverTests(unittest.TestCase):
    def test_checkpoints_after_cutover_do_not_add_post_upgrade_messages(self):
        plan = plan_agent_history(fixture(), "2026-09-01T00:00:08Z")
        self.assertEqual(
            [a["content"]["text"] for a in plan["artifacts"]],
            ["Inventory?", "500 units", "BOM?"],
        )


class AgentContentRevisionTests(unittest.TestCase):
    def test_final_content_time_changes_without_extending_unchanged_messages(self):
        rows = fixture()
        blob = next(r["row"] for r in reversed(rows) if r["kind"] == "agent_blob")
        messages = decode_messages(blob["blob_hex"])
        messages[1]["content"] = "Corrected: 600 units"
        blob["blob_hex"] = msgpack.packb(messages, use_bin_type=True).hex()
        plan = plan_agent_history(rows, "2026-10-01T00:00:00Z")
        first = next(i for i in plan["core"]["interactions"] if i["ordinal"] == 1)
        self.assertEqual(first["created_at"], "2026-09-01T00:00:01.000000Z")
        self.assertEqual(first["terminal_at"], "2026-09-01T00:00:10.000000Z")
        artifact = next(
            a
            for a in plan["artifacts"]
            if a["interaction_id"] == first["interaction_id"]
            and a["artifact_type"] == "result"
        )
        self.assertEqual(artifact["content"]["text"], "Corrected: 600 units")
        self.assertEqual(artifact["observed_at"], first["terminal_at"])
