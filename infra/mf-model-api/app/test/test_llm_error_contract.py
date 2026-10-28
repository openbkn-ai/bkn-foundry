"""Tests for test_llm_error_contract."""
import asyncio
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
    """Test mock session."""
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


class TestOtherClientNonStreamMetering:
    @pytest.mark.asyncio
    async def test_success_records_status_tokens_and_elapsed_time(self):
        response = {
            "choices": [{"message": {"content": "OK"}}],
            "usage": {"prompt_tokens": 76, "completion_tokens": 36, "total_tokens": 112},
        }
        session_cm, _ = _mock_session(200, json.dumps(response))
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.time, 'perf_counter', side_effect=[100.0, 102.547]), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            result = await _other_client().chat_completion(MESSAGES, "user1", "test")

        assert result["choices"][0]["message"]["content"] == "OK"
        audit.assert_awaited_once()
        record = audit.await_args.args[0]
        assert record.status == "success"
        assert record.total_time == pytest.approx(2.547)
        assert record.input_tokens == 76
        assert record.output_tokens == 36

    @pytest.mark.asyncio
    @pytest.mark.parametrize("status", [400, 401, 403, 404, 429, 500, 503, 504])
    async def test_upstream_error_records_one_failure(self, status):
        session_cm, session = _mock_session(
            status, '{"error":{"message":"controlled provider failure"}}')
        response = session.post.return_value.__aenter__.return_value
        response.headers = {"Retry-After": "7"}
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.time, 'perf_counter', side_effect=[100.0, 102.5]), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            result = await _other_client().chat_completion(MESSAGES, "user1", "test")

        assert llm_utils.openai_error.is_error(result)
        assert result["_http_status"] == openai_error.http_status_for(status)
        if status in (429, 503):
            assert result["_retry_after"] == 7
        assert session.post.call_count == 1
        audit.assert_awaited_once()
        record = audit.await_args.args[0]
        assert record.status == "failed"
        assert record.model_id == "1"
        assert record.user_id == "user1"
        assert record.input_tokens == record.output_tokens == 0
        assert record.total_time == pytest.approx(2.5)

    @pytest.mark.asyncio
    @pytest.mark.parametrize("failure", [
        asyncio.TimeoutError("controlled timeout"),
        llm_utils.aiohttp.ClientConnectionError("controlled connection failure"),
        json.JSONDecodeError("invalid provider response", "invalid", 0),
    ])
    async def test_exhausted_retries_record_one_failure(self, failure):
        session_cm, session = _mock_session(200, "")
        response = session.post.return_value.__aenter__.return_value
        response.text.side_effect = failure
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.asyncio, 'sleep', new_callable=AsyncMock) as sleeper, \
                patch.object(llm_utils.time, 'perf_counter', side_effect=[100.0, 105.0]), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            with pytest.raises(type(failure)) as raised:
                await _other_client().chat_completion(MESSAGES, "user1", "test")

        assert raised.value is failure
        assert session.post.call_count == 3
        assert sleeper.await_count == 2
        audit.assert_awaited_once()
        record = audit.await_args.args[0]
        assert record.status == "failed"
        assert record.input_tokens == record.output_tokens == 0
        assert record.total_time == pytest.approx(5.0)

    @pytest.mark.asyncio
    async def test_response_processing_failure_is_not_counted_as_success(self):
        session_cm, session = _mock_session(200, json.dumps({
            "choices": [],
            "usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
        }))
        client = _other_client()
        client.model_type = "rlm"
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.asyncio, 'sleep', new_callable=AsyncMock), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            with pytest.raises(IndexError):
                await client.chat_completion(MESSAGES, "user1", "test")

        assert session.post.call_count == 3
        audit.assert_awaited_once()
        assert audit.await_args.args[0].status == "failed"

    @pytest.mark.asyncio
    async def test_local_role_rejection_does_not_record_provider_usage(self):
        client = _other_client()
        client.api_model = "qianxun-test"
        with patch.object(llm_utils.aiohttp, 'ClientSession') as session, \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            result = await client.chat_completion(
                [{"role": "assistant", "content": "invalid final role"}], "user1", "test")

        assert result["_http_status"] == 400
        session.assert_not_called()
        audit.assert_not_awaited()

    @pytest.mark.asyncio
    async def test_recovered_retry_records_only_success(self):
        body = json.dumps({
            "choices": [{"message": {"content": "OK"}}],
            "usage": {"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
        })
        session_cm, session = _mock_session(200, body)
        response = session.post.return_value.__aenter__.return_value
        response.text.side_effect = [asyncio.TimeoutError(), body]
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils.asyncio, 'sleep', new_callable=AsyncMock), \
                patch.object(llm_utils.time, 'perf_counter', side_effect=[100.0, 105.0]), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit:
            result = await _other_client().chat_completion(MESSAGES, "user1", "test")

        assert result["choices"][0]["message"]["content"] == "OK"
        assert session.post.call_count == 2
        audit.assert_awaited_once()
        record = audit.await_args.args[0]
        assert record.status == "success"
        assert record.input_tokens == 3
        assert record.output_tokens == 2
        assert record.total_time == pytest.approx(5.0)


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
        """Test test upstream busy yields openai error frame."""
        session_cm, session = _mock_session(503, BUSY_BODY)
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock) as audit, \
                patch.object(llm_utils, 'sleep_before_retry', new_callable=AsyncMock):
            chunks = await _collect(_other_client().chat_completion_stream_openai(
                MESSAGES, "user1", True, {}, "test"))

        assert len(chunks) == 1
        body = json.loads(chunks[0])
        assert body["error"]["message"].startswith("Service is too busy.")
        assert body["error"]["type"] == "service_unavailable_error"
        assert session.post.call_count == 3
        audit.assert_awaited_once()
        assert audit.await_args.args[0].status == "failed"
        # No traces of the old shape should remain.
        assert "code" not in body or body["code"] != "ModelFactory.ModelController.Model.Error"
        assert "description" not in body
        assert "solution" not in body
        assert not chunks[0].startswith("--error--")

    @pytest.mark.asyncio
    async def test_retryable_status_retries_then_gives_up(self):
        """Test test retryable status retries then gives up."""
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
        """Test test client error status does not retry."""
        session_cm, session = _mock_session(400, '{"error":{"message":"bad model"}}')
        with patch.object(llm_utils.aiohttp, 'ClientSession', return_value=session_cm), \
                patch.object(llm_utils, 'add_llm_model_call_log', new_callable=AsyncMock), \
                patch.object(llm_utils, 'sleep_before_retry', new_callable=AsyncMock) as sleeper:
            chunks = await _collect(_other_client().chat_completion_stream_openai(
                MESSAGES, "user1", True, {}, "test"))

        assert session.post.call_count == 1
        assert sleeper.await_count == 0
        assert json.loads(chunks[-1])["error"]["message"] == "bad model"


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
