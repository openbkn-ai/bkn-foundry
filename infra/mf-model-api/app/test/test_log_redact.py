"""日志脱敏（#636）：凭据与用户内容不得离开本服务进入日志链路。"""
import pytest

from app.utils import log_redact

REAL_KEY = "sk-abcdef0123456789abcdef0123456789"


class TestMaskSecret:
    def test_keeps_scheme_and_prefix(self):
        masked = log_redact.mask_secret(f"Bearer {REAL_KEY}")
        assert masked.startswith("Bearer sk-a")
        assert REAL_KEY not in masked

    def test_bare_token(self):
        masked = log_redact.mask_secret(REAL_KEY)
        assert REAL_KEY not in masked
        assert masked.startswith("sk-a")

    @pytest.mark.parametrize("value", ["", None, 123, "abc"])
    def test_degenerate_values_never_pass_through(self, value):
        assert log_redact.mask_secret(value) == "***"

    def test_length_hint_survives(self):
        """长度对「是不是配错了半截 key」这类排障有用，本身不敏感"""
        assert f"({len(REAL_KEY)})" in log_redact.mask_secret(REAL_KEY)


class TestSafeHeaders:
    def test_authorization_masked(self):
        safe = log_redact.safe_headers({
            "Authorization": f"Bearer {REAL_KEY}",
            "Content-Type": "application/json"})
        assert REAL_KEY not in str(safe)
        assert safe["Content-Type"] == "application/json"

    @pytest.mark.parametrize("name", [
        "Authorization", "authorization", "api-key", "API-KEY",
        "x-api-key", "apikey", "secret-key", "Cookie",
        "proxy-authorization",
    ])
    def test_sensitive_names_case_insensitive(self, name):
        safe = log_redact.safe_headers({name: REAL_KEY})
        assert REAL_KEY not in str(safe)

    def test_non_dict_is_safe(self):
        assert log_redact.safe_headers(None) == {}
        assert log_redact.safe_headers("Bearer x") == {}


class TestRequestDigest:
    PARAMS = {
        "messages": [
            {"role": "system", "content": "你是助手"},
            {"role": "user", "content": "张伟的身份证号是多少"},
        ],
        "model": "qwen3.7-plus",
        "stream": True,
        "max_tokens": 1024,
        "temperature": 0.7,
        "top_p": 0.9,
        "tools": [{"type": "function"}],
    }

    def test_no_user_content_leaks(self):
        digest = str(log_redact.request_digest(self.PARAMS))
        assert "身份证" not in digest
        assert "你是助手" not in digest

    def test_keeps_what_triage_needs(self):
        digest = log_redact.request_digest(self.PARAMS)
        assert digest["model"] == "qwen3.7-plus"
        assert digest["stream"] is True
        assert digest["message_count"] == 2
        assert digest["roles"] == ["system", "user"]
        assert digest["max_tokens"] == 1024
        assert digest["temperature"] == 0.7
        assert digest["tool_count"] == 1

    def test_size_without_content(self):
        """规模能看出「是不是超长导致的」，但看不到内容本身"""
        expected = len("你是助手") + len("张伟的身份证号是多少")
        digest = log_redact.request_digest(self.PARAMS)
        assert digest["message_chars"] == expected

    def test_absent_params_are_omitted(self):
        digest = log_redact.request_digest({"messages": []})
        assert "max_tokens" not in digest
        assert "tool_count" not in digest
        assert digest["message_count"] == 0

    def test_tolerates_junk(self):
        assert log_redact.request_digest(None) == {}
        assert log_redact.request_digest(
            {"messages": "not a list"})["message_count"] == 0
        assert log_redact.request_digest(
            {"messages": [{"role": "user", "content": None}, "junk"]}
        )["message_chars"] == 0

    def test_messages_digest_shortcut(self):
        digest = log_redact.messages_digest(self.PARAMS["messages"])
        assert digest["message_count"] == 2
        assert "身份证" not in str(digest)


class TestCallSites:
    """光有 helper 不算修好——泄露点必须真的换掉了"""

    def test_no_raw_headers_or_payload_in_logs(self):
        for path in ("app/utils/llm_utils.py",
                     "app/controller/llm_controller.py"):
            with open(path, encoding="utf-8") as f:
                src = f.read()
            assert "headers={headers}" not in src, path
            assert "payload={params}" not in src, path
            assert "params={messages}" not in src, path
