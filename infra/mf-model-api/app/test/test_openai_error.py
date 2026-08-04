"""OpenAI 兼容面错误契约（#620）

/v1/chat/completions 上的失败必须是 {"error": {...}}，客户端才认得。
"""
import json

import pytest

from app.utils import openai_error


# #620 现场抓到的两种上游错误壳
UPSTREAM_FLAT = '{"code":50508,"message":"System is too busy now. Please try again later.","data":null}'
UPSTREAM_OPENAI = ('{"error":{"message":"Service is too busy. We advise users to temporarily '
                   'switch to alternative LLM API service providers.","type":"service_unavailable_error",'
                   '"param":null,"code":"service_unavailable_error"}}')


class TestFromUpstream:
    def test_flat_code_message(self):
        """扁平 {code, message}：message 提到 error.message，code 保留"""
        body = openai_error.from_upstream(UPSTREAM_FLAT, 503)
        assert body["error"]["message"] == "System is too busy now. Please try again later."
        assert body["error"]["code"] == 50508
        assert body["error"]["type"] == "service_unavailable_error"

    def test_openai_shape_passthrough(self):
        """上游已经合规就原样透传，不再套壳、不再二次编码"""
        body = openai_error.from_upstream(UPSTREAM_OPENAI, 503)
        assert body["error"]["message"].startswith("Service is too busy.")
        assert body["error"]["type"] == "service_unavailable_error"
        assert body["error"]["code"] == "service_unavailable_error"

    def test_no_double_encoding(self):
        """message 必须是句人话，不能是 JSON 字符串套 JSON"""
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
        assert body["error"]["message"] == "模型服务调用失败"

    def test_accepts_dict(self):
        body = openai_error.from_upstream({"detail": "boom"}, 500)
        assert body["error"]["message"] == "boom"

    def test_shape_is_complete(self):
        """四个字段一个都不能少，errorSchema 才过得去"""
        body = openai_error.from_upstream(UPSTREAM_FLAT, 429)
        assert set(body) == {"error"}
        assert set(body["error"]) == {"message", "type", "param", "code"}
        assert isinstance(body["error"]["message"], str)


class TestStatusMapping:
    @pytest.mark.parametrize("upstream,expected", [
        (429, 429), (400, 400), (401, 401), (403, 403), (404, 404),
        (500, 503), (502, 503), (503, 503), (504, 503),
        (418, 400), (None, 502),
    ])
    def test_http_status_for(self, upstream, expected):
        assert openai_error.http_status_for(upstream) == expected

    def test_busy_is_never_200(self):
        """#620 的核心：上游忙不能对外报成功"""
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
        """私有键只在进程内传状态码，出门前必须 pop 干净"""
        body = openai_error.with_http_status(
            openai_error.build_error("busy"), 429, retry_after=7)
        assert openai_error.pop_http_status(body) == 429
        assert openai_error.pop_retry_after(body) == 7
        assert set(body) == {"error"}

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
        """老代码发的是 '--error--{...}'，不是合法 JSON，客户端直接崩"""
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
        """老 envelope 顶层没有 error，正是它骗过客户端 union 的原因"""
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
