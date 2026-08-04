"""日志脱敏（#636）：凭据与用户内容不得离开本服务进入日志链路。"""
import os
import re

import pytest

from app.utils import log_redact

# 相对测试文件定位源码，不依赖 pytest 的启动目录
_APP_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

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


class TestSafeUrl:
    def test_query_is_stripped(self):
        """百度 oauth 把 client_secret 拼在 query 里，OtherClient 的 api_url
        又是管理员自由填的——不能假设 query 里没有凭据"""
        url = ("https://aip.baidubce.com/oauth/2.0/token"
               "?grant_type=client_credentials&client_id=ak&client_secret=sk-xyz")
        safe = log_redact.safe_url(url)
        assert safe == "https://aip.baidubce.com/oauth/2.0/token?***"
        assert "sk-xyz" not in safe

    def test_plain_url_untouched(self):
        url = "https://api.example.com/v1/chat/completions"
        assert log_redact.safe_url(url) == url

    @pytest.mark.parametrize("value", ["", None, 123])
    def test_degenerate(self, value):
        assert log_redact.safe_url(value) == ""


class TestCallSites:
    """光有 helper 不算修好——泄露点必须真的换掉了。

    按模式匹配而非字面量，`headers={headers!r}` 这类改写也拦得住。
    """

    SOURCES = ("utils/llm_utils.py", "controller/llm_controller.py")

    # 把「原始变量直接进 f-string」的写法一网打尽
    RAW_PATTERNS = (
        r"\{headers[!:}]",
        r"\{params[!:}]",
        r"\{messages[!:}]",
        r"json\.dumps\(messages",
    )

    @pytest.mark.parametrize("rel", SOURCES)
    def test_no_raw_context_in_log_calls(self, rel):
        with open(os.path.join(_APP_ROOT, rel), encoding="utf-8") as f:
            lines = f.readlines()

        offenders = []
        for i, line in enumerate(lines, 1):
            # 只看日志调用所在的行及其续行（f-string 常被折行）
            window = "".join(lines[max(0, i - 4):i])
            if not re.search(r"(StandLogger|get_logger\(\))\.[a-z_]+\(", window):
                continue
            for pattern in self.RAW_PATTERNS:
                if re.search(pattern, line):
                    offenders.append(f"{rel}:{i}: {line.strip()}")
        assert not offenders, "原始上下文直接进日志：\n" + "\n".join(offenders)

    def test_guard_actually_catches_regressions(self):
        """守卫本身得有区分度，否则全绿只是错觉"""
        import re as _re
        bad = 'StandLogger.error(f"x headers={headers}")'
        assert any(_re.search(p, bad) for p in self.RAW_PATTERNS)
