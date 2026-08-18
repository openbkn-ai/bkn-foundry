"""Tests for test_openai_error."""
import json

import pytest

from app.commons.locale import reset_effective_locale, set_effective_locale
from app.utils import openai_error


# Two upstream error envelopes captured from the #620 incident.
UPSTREAM_FLAT = '{"code":50508,"message":"System is too busy now. Please try again later.","data":null}'
UPSTREAM_OPENAI = ('{"error":{"message":"Service is too busy. We advise users to temporarily '
                   'switch to alternative LLM API service providers.","type":"service_unavailable_error",'
                   '"param":null,"code":"service_unavailable_error"}}')
ZH_CONNECTION_FAILED = "无法连接到模型服务。"


class TestFromUpstream:
    def test_flat_code_message(self):
        """Test test flat code message."""
        body = openai_error.from_upstream(UPSTREAM_FLAT, 503)
        assert body["error"]["message"] == "System is too busy now. Please try again later."
        assert body["error"]["code"] == 50508
        assert body["error"]["type"] == "service_unavailable_error"

    def test_openai_shape_passthrough(self):
        """Test test openai shape passthrough."""
        body = openai_error.from_upstream(UPSTREAM_OPENAI, 503)
        assert body["error"]["message"].startswith("Service is too busy.")
        assert body["error"]["type"] == "service_unavailable_error"
        assert body["error"]["code"] == "service_unavailable_error"

    def test_no_double_encoding(self):
        """Test test no double encoding."""
        for payload in (UPSTREAM_FLAT, UPSTREAM_OPENAI):
            message = openai_error.from_upstream(payload, 503)["error"]["message"]
            with pytest.raises(ValueError):
                json.loads(message)

    def test_plain_text_body(self):
        body = openai_error.from_upstream("upstream exploded", 500)
        assert body["error"]["message"] == "upstream exploded"
        assert body["error"]["type"] == "server_error"

    def test_empty_body_falls_back(self):
        body = openai_error.from_upstream("", 502)
        assert body["error"]["message"] == ZH_CONNECTION_FAILED

    def test_empty_body_fallback_uses_effective_english_locale(self):
        token = set_effective_locale("en-US")
        try:
            body = openai_error.from_upstream("", 502)
        finally:
            reset_effective_locale(token)
        assert body["error"]["message"] == "The model service could not be reached."

    def test_unknown_dict_shape_does_not_leak_whole_body(self):
        """Test test unknown dict shape does not leak whole body."""
        body = openai_error.from_upstream(
            {"trace_id": "abc", "node": "gw-internal-3",
             "request": {"api_key": "sk-secret"}}, 500)
        assert body["error"]["message"] == ZH_CONNECTION_FAILED
        assert "sk-secret" not in json.dumps(body)
        assert "gw-internal-3" not in json.dumps(body)

    def test_empty_upstream_message_falls_back(self):
        body = openai_error.from_upstream(
            '{"error":{"message":"","code":"x"}}', 500)
        assert body["error"]["message"] == ZH_CONNECTION_FAILED

    def test_html_error_page_falls_back(self):
        body = openai_error.from_upstream(
            "<html><head><title>502 Bad Gateway</title></head></html>", 502)
        assert body["error"]["message"] == ZH_CONNECTION_FAILED

    def test_overlong_plain_body_falls_back(self):
        body = openai_error.from_upstream("x" * 900, 500)
        assert body["error"]["message"] == ZH_CONNECTION_FAILED

    def test_accepts_dict(self):
        body = openai_error.from_upstream({"detail": "boom"}, 500)
        assert body["error"]["message"] == "boom"

    def test_shape_is_complete(self):
        """Test test shape is complete."""
        body = openai_error.from_upstream(UPSTREAM_FLAT, 429)
        assert set(body) == {"error"}
        assert set(body["error"]) == {"message", "type", "param", "code"}
        assert isinstance(body["error"]["message"], str)


class TestStatusMapping:
    @pytest.mark.parametrize("upstream,expected", [
        (429, 429), (400, 400), (408, 408), (413, 413), (422, 422),
        (500, 503), (502, 503), (503, 503), (504, 503),
        (418, 400), (None, 502),
    ])
    def test_http_status_for(self, upstream, expected):
        assert openai_error.http_status_for(upstream) == expected

    @pytest.mark.parametrize("upstream", [401, 403, 404])
    def test_dependency_auth_status_never_leaks(self, upstream):
        """Test test dependency auth status never leaks."""
        assert openai_error.http_status_for(upstream) == 502

    @pytest.mark.parametrize("upstream,expected_type", [
        (401, "authentication_error"),
        (403, "permission_error"),
        (404, "not_found_error"),
    ])
    def test_real_cause_survives_in_error_type(self, upstream, expected_type):
        """Test test real cause survives in error type."""
        body = openai_error.from_upstream("nope", upstream)
        assert body["error"]["type"] == expected_type

    def test_busy_is_never_200(self):
        """Test test busy is never 200."""
        assert openai_error.http_status_for(503) != 200
        assert openai_error.http_status_for(429) != 200

    @pytest.mark.parametrize("status,expected", [
        (429, "rate_limit_exceeded"),
        (503, "service_unavailable_error"),
        (400, "invalid_request_error"),
        (None, "api_connection_error"),
    ])
    def test_error_type_for_status(self, status, expected):
        assert openai_error.error_type_for_status(status) == expected

    @pytest.mark.parametrize("status,retryable", [
        (429, True), (502, True), (503, True), (504, True),
        (400, False), (401, False), (404, False), (500, False),
    ])
    def test_is_retryable(self, status, retryable):
        assert openai_error.is_retryable(status) is retryable


class TestPrivateKeys:
    def test_round_trip_and_cleanup(self):
        """Test test round trip and cleanup."""
        body = openai_error.with_http_status(
            openai_error.build_error("busy"), 429, retry_after=7)
        assert openai_error.pop_http_status(body) == 429
        assert openai_error.pop_retry_after(body) == 7
        assert set(body) == {"error"}

    @pytest.mark.parametrize("upstream,expect_header", [
        (429, True), (503, True), (500, True),
        (401, False), (403, False), (404, False), (400, False),
    ])
    def test_retry_after_only_when_waiting_helps(self, upstream, expect_header):
        """Test test retry after only when waiting helps."""
        body = openai_error.with_http_status(
            openai_error.build_error("x"), upstream, retry_after=7)
        assert (openai_error.pop_retry_after(body) == 7) is expect_header

    def test_public_copy_strips_private_keys(self):
        """Test test public copy strips private keys."""
        body = openai_error.with_http_status(
            openai_error.build_error("busy"), 429, retry_after=5)
        public = openai_error.public_copy(body)
        assert set(public) == {"error"}
        # Keep the private body intact until the caller consumes the HTTP status.
        assert openai_error.pop_http_status(body) == 429

    def test_defaults_when_absent(self):
        body = openai_error.build_error("boom")
        assert openai_error.pop_http_status(body, 500) == 500
        assert openai_error.pop_retry_after(body) is None

    def test_retry_after_follows_upstream_header(self):
        assert openai_error.retry_after_seconds(429, {"Retry-After": "12"}) == 12
        assert openai_error.retry_after_seconds(429, {"retry-after": "3"}) == 3
        assert openai_error.retry_after_seconds(429, None) == 5
        assert openai_error.retry_after_seconds(400, None) is None
        assert openai_error.retry_after_seconds(429, {"Retry-After": "junk"}) == 5


class TestFromEnvelope:
    def test_keeps_code_drops_solution(self):
        envelope = {
            "code": "ModelFactory.ExternalSmallModel.Used.NameNotExist",
            "description": "模型不存在",
            "detail": "模型不存在",
            "solution": "请检查配置信息",
            "link": "",
        }
        body = openai_error.from_envelope(envelope, 400)
        assert body["error"]["message"] == "模型不存在"
        assert body["error"]["code"] == "ModelFactory.ExternalSmallModel.Used.NameNotExist"
        assert body["error"]["type"] == "invalid_request_error"
        assert "solution" not in body["error"]
        assert "link" not in body["error"]

    def test_falls_back_to_description(self):
        body = openai_error.from_envelope(
            {"code": "X", "detail": "", "description": "模型连接异常"}, 502)
        assert body["error"]["message"] == "模型连接异常"


class TestFrames:
    def test_error_frame_is_json(self):
        frame = openai_error.error_frame(openai_error.build_error("busy"))
        assert json.loads(frame)["error"]["message"] == "busy"

    def test_frame_has_no_legacy_prefix(self):
        """Test test frame has no legacy prefix."""
        frame = openai_error.error_frame(openai_error.build_error("busy"))
        assert not frame.startswith("--error--")

    @pytest.mark.parametrize("chunk,expected", [
        ('{"error":{"message":"busy"}}', True),
        ('{"choices":[{"delta":{"content":"hi"}}]}', False),
        ("[DONE]", False),
        ("", False),
        (None, False),
        (b'{"error":{"message":"busy"}}', True),
    ])
    def test_is_error_frame(self, chunk, expected):
        assert openai_error.is_error_frame(chunk) is expected

    def test_legacy_envelope_is_not_an_error_frame(self):
        """Test test legacy envelope is not an error frame."""
        legacy = json.dumps({
            "code": "ModelFactory.ModelController.Model.Error",
            "description": UPSTREAM_FLAT,
            "detail": UPSTREAM_FLAT,
            "solution": "请检查配置信息",
            "link": "",
        })
        assert openai_error.is_error(json.loads(legacy)) is False


class TestIsError:
    @pytest.mark.parametrize("payload,expected", [
        ({"error": {"message": "x"}}, True),
        ({"choices": []}, False),
        ({"error": "x"}, False),
        ("not a dict", False),
    ])
    def test_is_error(self, payload, expected):
        assert openai_error.is_error(payload) is expected
