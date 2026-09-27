import unittest
from types import SimpleNamespace
from unittest.mock import AsyncMock, patch

from app import evidence, observability
from app.core import graph, runner


class DerivedClaimScopeTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.context_token = observability.set_context(observability.build_context({}))

    async def asyncTearDown(self):
        observability.reset_context(self.context_token)

    async def _run_emitter(self, emit, **kwargs):
        submitted = AsyncMock(return_value=True)
        with (
            patch.object(evidence, "adopted_sources", return_value=(
                ["evt-source"], ["op-local-model"],
                [{"ref_type": "document", "ref": "doc-1"}], [],
            )),
            patch.object(evidence, "result_artifact", return_value={"artifact_id": "artifact-1"}),
            patch.object(evidence, "artifact_ref", return_value="artifact-1"),
            patch.object(evidence, "submit_artifact", new=AsyncMock(return_value=True)),
            patch.object(evidence, "claim_created", return_value={"event_id": "evt-claim", "event_type": "claim.created"}),
            patch.object(evidence, "submit_events", new=submitted),
        ):
            await emit(**kwargs)
        events = submitted.await_args.args[0]
        self.assertEqual(
            [item["event_type"] for item in events],
            ["claim.created", "evidence.refs.created", "business.refs.resolved"],
        )
        for item in events[1:]:
            self.assertNotIn("operation_id", item)
            self.assertEqual(item["causation_event_id"], "evt-claim")

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
