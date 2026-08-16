"""#620 回归：/v1/chat/completions 失败出口必须是 OpenAI 错误契约。

客户端（@ai-sdk/openai-compatible、openai-python、LangChain）按
union(chunkSchema, errorSchema) 解析，模型工厂 envelope 两边都不匹配，
会把整段 zod 报错糊到用户脸上。
"""
import json
from unittest.mock import AsyncMock, Mock, patch

import pytest

from app.controller.llm_controller import (envelope_error_response,
                                           openai_error_response)
from app.utils import llm_utils, openai_error

BUSY_BODY = ('{"error":{"message":"Service is too busy. We advise users to temporarily '
             'switch to alternative LLM API service providers.","type":"service_unavailable_error",'
             '"param":null,"code":"service_unavailable_error"}}')


def _mock_session(status, body):
    """构造 aiohttp.ClientSession mock，返回 (session_cm, session)"""
    response = AsyncMock()
    response.status = status
    response.text = AsyncMock(return_value=body)
    response.headers = {}

    post_cm = AsyncMock()
    post_cm.__aenter__ = AsyncMock(return_value=response)
    post_cm.__aexit__ = AsyncMock(return_value=None)

    session = AsyncMock()
    session.post = Mock(return_value=post_cm)

    session_cm = AsyncMock()
    session_cm.__aenter__ = AsyncMock(return_value=session)
    session_cm.__aexit__ = AsyncMock(return_value=None)
    return session_cm, session


def _other_client():
    return llm_utils.OtherClient(
        api_url="http://upstream/v1/chat/completions",
        api_model="DeepSeek-V4-Flash", api_key="k", model_id="1",
        temperature=0.7, top_p=0.9, frequency_penalty=0,
        presence_penalty=0, max_tokens=100)


async def _collect(stream):
    return [chunk async for chunk in stream]


MESSAGES = [{"role": "user", "content": "hi"}]


class TestStreamErrorFrame:
    @pytest.mark.asyncio
    async def test_legacy_stream_logs_raw_connection_error_before_returning_safe_body(self):
        session_cm = AsyncMock()
        session_cm.__aenter__ = AsyncMock(
            side_effect=llm_utils.aiohttp.ClientConnectionError("connection refused"))
        session_cm.__aexit__ = AsyncMock(return_value=None)

        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.asyncio, 'sleep', new_callable=AsyncMock), \
                patch.object(llm_utils.StandLogger, 'error') as error_log:
            chunks = await _collect(_other_client().chat_completion_stream(
                MESSAGES, "user1", False, {}))

        assert len(chunks) == 1
        assert json.loads(chunks[0])["code"] == "ModelFactory.ModelController.Model.Error"
        assert any("connection refused" in str(call.args[0]) for call in error_log.call_args_list)

    @pytest.mark.asyncio
    async def test_upstream_busy_yields_openai_error_frame(self):
        """上游 503 忙：发合规错误帧，不发 envelope"""
        session_cm, session = _mock_session(503, BUSY_BODY)
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock), \
                patch.object(llm_utils, 'sleep_before_retry', new_callable=AsyncMock):
            chunks = await _collect(_other_client().chat_completion_stream_openai(
                MESSAGES, "user1", True, {}, "test"))

        assert len(chunks) == 1
        body = json.loads(chunks[0])
        assert body["error"]["message"].startswith("Service is too busy.")
        assert body["error"]["type"] == "service_unavailable_error"
        # 老形态的痕迹一个都不能剩
        assert "code" not in body or body["code"] != "ModelFactory.ModelController.Model.Error"
        assert "description" not in body
        assert "solution" not in body
        assert not chunks[0].startswith("--error--")

    @pytest.mark.asyncio
    async def test_retryable_status_retries_then_gives_up(self):
        """429/503 这类瞬态错误先退避重试，重试用完才报错"""
        session_cm, session = _mock_session(429, '{"code":50508,"message":"System is too busy now."}')
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock), \
                patch.object(llm_utils, 'sleep_before_retry', new_callable=AsyncMock) as sleeper:
            chunks = await _collect(_other_client().chat_completion_stream_openai(
                MESSAGES, "user1", True, {}, "test"))

        assert session.post.call_count == 3
        assert sleeper.await_count == 2
        body = json.loads(chunks[-1])
        assert body["error"]["message"] == "System is too busy now."
        assert body["error"]["code"] == 50508

    @pytest.mark.asyncio
    async def test_client_error_status_does_not_retry(self):
        """4xx 参数类错误重试没有意义，一次就报"""
        session_cm, session = _mock_session(400, '{"error":{"message":"bad model"}}')
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock), \
                patch.object(llm_utils, 'sleep_before_retry', new_callable=AsyncMock) as sleeper:
            chunks = await _collect(_other_client().chat_completion_stream_openai(
                MESSAGES, "user1", True, {}, "test"))

        assert session.post.call_count == 1
        assert sleeper.await_count == 0
        assert json.loads(chunks[-1])["error"]["message"] == "bad model"


class TestTraceStream:
    @pytest.mark.asyncio
    async def test_error_frame_marks_trace_failed(self):
        """错误帧终止的流不能记成 success"""
        client = Mock()
        client._emit_bkn_trace_evidence = Mock()

        async def stream():
            yield openai_error.error_frame(openai_error.build_error("busy"))

        await _collect(llm_utils.trace_model_stream(client, stream(), MESSAGES, {}))

        assert client._emit_bkn_trace_evidence.call_count == 1
        assert client._emit_bkn_trace_evidence.call_args.kwargs["status"] == "failed"

    @pytest.mark.asyncio
    async def test_clean_stream_still_success(self):
        client = Mock()
        client._emit_bkn_trace_evidence = Mock()

        async def stream():
            yield '{"choices":[{"delta":{"content":"hi"}}]}'

        await _collect(llm_utils.trace_model_stream(client, stream(), MESSAGES, {}))

        assert client._emit_bkn_trace_evidence.call_args.kwargs["status"] == "success"


class TestControllerResponses:
    def _body(self, response):
        return json.loads(response.body.decode("utf-8"))

    def test_error_response_uses_upstream_status_and_retry_after(self):
        error_body = openai_error.with_http_status(
            openai_error.build_error("busy", error_type="rate_limit_exceeded"),
            429, retry_after=5)
        response = openai_error_response(error_body, 500)

        assert response.status_code == 429
        assert response.headers["retry-after"] == "5"
        body = self._body(response)
        assert body["error"]["message"] == "busy"
        assert "_http_status" not in body and "_retry_after" not in body

    def test_error_response_falls_back_to_default_status(self):
        response = openai_error_response(openai_error.build_error("boom"), 502)
        assert response.status_code == 502

    def test_envelope_response_is_openai_shaped(self):
        envelope = {
            "code": "ModelFactory.ModelController.Model.ConnectError",
            "description": "模型连接异常", "detail": "模型连接异常",
            "solution": "请检查模型配置信息", "link": "",
        }
        response = envelope_error_response(envelope, 502)

        assert response.status_code == 502
        body = self._body(response)
        assert body == {"error": {
            "message": "模型连接异常",
            "type": "service_unavailable_error",
            "param": None,
            "code": "ModelFactory.ModelController.Model.ConnectError",
        }}
