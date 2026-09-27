import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app import evidence, observability
from app.core import graph, runner


class DerivedClaimScopeTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.context_token = observability.set_context(observability.build_context({}))
        self.interaction_token = evidence.begin_interaction(
            "intent", "chat", "agent-1", "bkn.agent.chat",
            conversation_id="conv-scope-1", interaction_id="int-scope-1",
        )

    async def asyncTearDown(self):
        evidence.end_interaction(self.interaction_token)
        observability.reset_context(self.context_token)

    async def _run_emitter(self, emit, **kwargs):
        submitted = AsyncMock(return_value=True)
        business_ref = {
            "ref_id": "object:knowledge_network:purchase_order",
            "ref_type": "object_type",
            "source_system": "context-loader",
            "validity": "observed",
            "version_status": "v1",
            "visibility": "visible",
        }
        with (
            patch.object(evidence, "adopted_sources", return_value=(
                ["evt-source"], ["op-local-model"],
                [business_ref], [business_ref],
            )),
            patch.object(evidence, "result_artifact", return_value={"artifact_id": "artifact-1"}),
            patch.object(evidence, "artifact_ref", return_value="artifact-1"),
            patch.object(evidence, "submit_artifact", new=AsyncMock(return_value=True)),
            patch.object(evidence, "submit_events", new=submitted),
        ):
            await emit(**kwargs)
        events = submitted.await_args.args[0]
        self.assertEqual(
            [item["event_type"] for item in events],
            ["claim.created", "evidence.refs.created", "business.refs.resolved"],
        )
        claim_event_id = events[0]["event_id"]
        self.assertEqual(events[0]["payload"]["operation_ids"], ["op-local-model"])
        self.assertTrue(all(item["interaction_id"] == "int-scope-1" for item in events))
        for item in events[1:]:
            self.assertNotIn("operation_id", item)
            self.assertEqual(item["causation_event_id"], claim_event_id)

        batch = evidence.build_batch(events, "user-1", "user")
        self.assertIsNotNone(batch)
        ledger_events = evidence.build_ledger_events(batch)
        self.assertEqual(len(ledger_events), 3)
        for item in ledger_events:
            self.assertEqual(item["conversation_id"], "conv-scope-1")
            self.assertEqual(item["interaction_id"], "int-scope-1")
        for item in ledger_events[1:]:
            self.assertNotIn("operation_id", item)
            self.assertNotIn("operation_business_edges", item)
            self.assertEqual(item["causation_event_ids"], [claim_event_id])
            self.assertIn(":interaction:int-scope-1:", item["producer_stream_id"])
        self.assertEqual(len(ledger_events[2]["business_refs"]), 1)

    async def test_chat_derived_claim_events_do_not_claim_local_model_operation(self):
        await self._run_emitter(
            graph._emit_chat_evidence,
            agent=SimpleNamespace(agent_id="agent-1"),
            thread_id="thread-1",
            prompt_source="default",
            prompt_version="1",
            account_id="user-1",
            account_type="user",
            output="answer",
            claim_type="answer",
            response_format=None,
            structured_validation_path=None,
            tool_names=[],
        )

    async def test_task_derived_claim_events_do_not_claim_local_model_operation(self):
        await self._run_emitter(
            runner._emit_task_evidence,
            agent=SimpleNamespace(agent_id="agent-1"),
            task_id="task-1",
            prompt_source="default",
            prompt_version="1",
            account_id="user-1",
            account_type="user",
            output="answer",
            claim_type="answer",
            response_format=None,
            structured_validation_path=None,
            result_messages=[],
        )


if __name__ == "__main__":
    unittest.main()
