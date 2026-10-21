"""Regression coverage for Claude message adaptation in both response modes."""
from copy import deepcopy
from unittest import IsolatedAsyncioTestCase
from unittest.mock import AsyncMock, Mock, patch

from app.utils import llm_utils


class RequestCaptured(Exception):
    """Stop at the HTTP boundary after inspecting the outgoing request."""


class TestClaudeMessages(IsolatedAsyncioTestCase):
    async def _assert_payload(self, messages, expected_messages, system=None,
                              expected_system=None):
        original_messages = deepcopy(messages)
        original_system = deepcopy(system)
        for stream in (False, True):
            with self.subTest(stream=stream):
                client = llm_utils.ClaudeClient(
                    api_url="http://upstream/v1/messages", api_model="s-haiku",
                    api_key="test-key", model_id="1", temperature=1, top_p=1,
                    frequency_penalty=0, presence_penalty=0, max_tokens=16384,
                    system=system,
                )
                session = Mock()
                session.post.side_effect = RequestCaptured
                session_cm = AsyncMock()
                session_cm.__aenter__.return_value = session
                with patch.object(llm_utils.aiohttp, "ClientSession", return_value=session_cm):
                    with self.assertRaises(RequestCaptured):
                        if stream:
                            async for _ in client.chat_completion_stream_openai(messages, "user1", "test"):
                                pass
                        else:
                            await client.chat_completion(messages, "user1", "test")
                payload = session.post.call_args.kwargs["json"]
                self.assertEqual(payload["messages"], expected_messages)
                if expected_system:
                    self.assertEqual(payload["system"], expected_system)
                else:
                    self.assertNotIn("system", payload)
                self.assertEqual(payload["stream"], stream)
                self.assertEqual(payload["model"], "s-haiku")
                self.assertEqual(payload["max_tokens"], 16384)
                self.assertEqual(messages, original_messages)
                self.assertEqual(system, original_system)

    async def test_reported_system_followed_by_seven_user_messages(self):
        users = [{"role": "user", "content": str(i)} for i in range(7)]
        await self._assert_payload(
            [{"role": "system", "content": "Instructions"}] + users, users,
            expected_system=[{"type": "text", "text": "Instructions"}],
        )

    async def test_multiple_system_messages_at_beginning_middle_and_end(self):
        user = {"role": "user", "content": "hi"}
        await self._assert_payload(
            [{"role": "system", "content": "first"},
             {"role": "system", "content": "second"}, user,
             {"role": "system", "content": "third"}], [user],
            expected_system=[{"type": "text", "text": text}
                             for text in ("first", "second", "third")],
        )

    async def test_top_level_system_preserves_cache_control(self):
        system = [{"type": "text", "text": "explicit", "cache_control": {"type": "ephemeral"}}]
        messages = [{"role": "user", "content": "hi"}]
        await self._assert_payload(messages, messages, system, system)

    async def test_both_system_forms_are_kept_in_order(self):
        system = [{"type": "text", "text": "explicit"}]
        user = {"role": "user", "content": "hi"}
        await self._assert_payload(
            [{"role": "system", "content": "from messages"}, user], [user],
            system, system + [{"type": "text", "text": "from messages"}],
        )

    async def test_system_content_blocks_are_preserved(self):
        blocks = [{"type": "text", "text": "first"}, {"type": "text", "text": "second"}]
        user = {"role": "user", "content": "hi"}
        await self._assert_payload(
            [{"role": "system", "content": blocks}, user], [user],
            expected_system=blocks,
        )

    async def test_single_system_keeps_legacy_user_fallback(self):
        await self._assert_payload(
            [{"role": "system", "content": "hi"}],
            [{"role": "user", "content": "hi"}],
        )

    async def test_without_system_preserves_multimodal_content(self):
        messages = [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]
        await self._assert_payload(messages, messages)

    async def test_tool_call_and_result_after_system_are_not_skipped(self):
        await self._assert_payload(
            [{"role": "system", "content": "use tools"},
             {"role": "assistant", "content": "", "tool_calls": [{
                 "id": "call1", "type": "function",
                 "function": {"name": "search", "arguments": '{"query":"hello"}'},
             }]},
             {"role": "tool", "tool_call_id": "call1", "content": "found"}],
            [{"role": "assistant", "content": [{
                "type": "tool_use", "id": "call1", "name": "search", "input": {"query": "hello"},
            }]},
             {"role": "user", "content": [{
                 "type": "tool_result", "tool_use_id": "call1", "content": "found",
             }]}],
            expected_system=[{"type": "text", "text": "use tools"}],
        )
