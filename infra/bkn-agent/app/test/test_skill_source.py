"""技能面从 capabilities-lab 收敛到执行工厂（#322）。

原实现打 capabilities-lab，而该服务只认 Authorization/Cookie，不读 bkn-agent 发的
x-account-id/x-account-type，导致挂了技能的 agent 一律 502。这里锁住新的调用契约。
"""
import asyncio

import pytest

from app.config import config
from app.core import skills


def test_normalize_skill_id_strips_prefix():
    # capabilities-lab 时代的 capability id 带 `skill:` 前缀，执行工厂要裸 uuid
    assert skills.normalize_skill_id("skill:abc-123") == "abc-123"
    assert skills.normalize_skill_id("abc-123") == "abc-123"


def test_strip_frontmatter():
    raw = "---\nname: demo\ndescription: d\n---\n\n# Body\n\ntext\n"
    assert skills.strip_frontmatter(raw) == "# Body\n\ntext"
    # 无 frontmatter 时原样返回，不能吞掉正文首行
    assert skills.strip_frontmatter("# Body\n\ntext\n") == "# Body\n\ntext"


class _FakeResp:
    def __init__(self, status=200, payload=None, text=""):
        self.status = status
        self._payload = payload
        self._text = text

    async def json(self):
        return self._payload

    async def text(self):
        return self._text

    async def __aenter__(self):
        return self

    async def __aexit__(self, *a):
        return False


class _FakeSession:
    """记录所有请求，按 URL 分发响应。"""

    def __init__(self):
        self.calls = []

    def get(self, url, headers=None):
        self.calls.append(("GET", url, headers, None))
        if "/content" in url:
            return _FakeResp(
                200,
                payload={
                    "url": "http://minio/skill.md",
                    "files": [
                        {"rel_path": "SKILL.md"},
                        {"rel_path": "reference.md"},
                    ],
                    "status": "published",
                },
            )
        return _FakeResp(200, text="---\nname: n\n---\n\n# 正文\n")

    def post(self, url, json=None, headers=None):
        self.calls.append(("POST", url, headers, json))
        return _FakeResp(200, payload={"url": "http://minio/ref.md"})


def test_fetch_skill_content_hits_execution_factory():
    sess = _FakeSession()
    out = asyncio.run(skills._fetch_skill_content(sess, "skill:sid-1", {"x-account-id": "a"}))

    meta_url = sess.calls[0][1]
    assert meta_url.startswith(config.OPERATOR_INTEGRATION_BASE), "必须打执行工厂，不是 capabilities-lab"
    assert "/internal-v1/skills/sid-1/content" in meta_url, "走 internal-v1，且 id 已剥前缀"
    # 第二跳取对象存储正文，且不得带业务身份头（presigned URL 自带签名）
    assert sess.calls[1][1] == "http://minio/skill.md"
    assert sess.calls[1][2] is None

    assert "# 正文" in out and "name: n" not in out, "frontmatter 应被剥掉"
    # 附属文件清单要进上下文，否则模型不知道能读什么、也不知道 skill_id
    assert "reference.md" in out and "sid-1" in out
    assert "SKILL.md" not in out, "正文本身已注入，不应再列进待读清单"


def test_fetch_skill_content_404_is_not_silent():
    class _NotFound(_FakeSession):
        def get(self, url, headers=None):
            return _FakeResp(404)

    with pytest.raises(Exception) as e:
        asyncio.run(skills._fetch_skill_content(_NotFound(), "sid-x", {}))
    assert getattr(e.value, "status_code", None) == 400


def test_read_skill_file_uses_rel_path():
    """原实现发 {"path": ...}，执行工厂要 rel_path（validate:"required"），每次必 400。"""
    from app.core.tools import _read_skill_file_tool

    tool = _read_skill_file_tool("acc", "user")
    sess = _FakeSession()

    import app.core.tools as tools_mod

    class _CM:
        def __init__(self, s):
            self.s = s

        async def __aenter__(self):
            return self.s

        async def __aexit__(self, *a):
            return False

    orig = tools_mod.aiohttp.ClientSession
    tools_mod.aiohttp.ClientSession = lambda *a, **k: _CM(sess)
    try:
        asyncio.run(tool.coroutine(skill_id="skill:sid-1", path="reference.md"))
    finally:
        tools_mod.aiohttp.ClientSession = orig

    method, url, _, body = sess.calls[0]
    assert method == "POST"
    assert "/internal-v1/skills/sid-1/files/read" in url
    assert body == {"rel_path": "reference.md"}, "字段名必须是 rel_path"
