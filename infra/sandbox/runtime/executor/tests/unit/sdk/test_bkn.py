"""sandbox_sdk.bkn 的单元测试。

不打真实网络：装配路径被替换成一份最小的假 stub，验证的是「凭据从哪来、缓存怎么
命中、失败时报什么」这些契约，而不是 BKN 那 21 个函数本身。
"""

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[4]))

import sandbox_sdk                      # noqa: E402
from sandbox_sdk import bkn             # noqa: E402

# 一份最小的能力面：只要有 _configure 和一个可调用的函数就够验证装配链路。
FAKE_STUB = '''
__toolkit_version__ = "sha256:fake"
_CFG = {}


def _configure(event):
    _CFG.clear()
    _CFG.update(event)


def whoami():
    return dict(_CFG)
'''


@pytest.fixture(autouse=True)
def reset():
    bkn.configure_runtime({})
    yield
    bkn.configure_runtime({})


@pytest.fixture
def fake_toolkit(monkeypatch, tmp_path):
    calls = []

    def _fetch():
        calls.append(1)
        return {"version": "sha256:fake", "stub": FAKE_STUB}

    monkeypatch.setattr(bkn, "_fetch_toolkit", _fetch)
    monkeypatch.setattr(bkn, "_CACHE_DIR", tmp_path / ".bkn-toolkit")
    return calls


def test_credentials_never_come_from_environment(monkeypatch):
    """令牌只认 event。

    沙箱会话是池化复用的，环境变量会把上一个调用方的值留在容器里；task_id 那类
    追踪标记漏了无伤大雅，令牌漏了是凭据泄露。所以这里必须报错，而不是回退。
    """
    monkeypatch.setenv("BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/")
    monkeypatch.setenv("token", "leaked-from-previous-caller")
    monkeypatch.setenv("BKN_TOKEN", "leaked-from-previous-caller")

    bkn.configure_runtime({})           # event 里没有 token
    assert bkn.available() is False
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn._token()
    assert "环境变量" in str(excinfo.value)


def test_mcp_url_may_fall_back_to_deployment_env(monkeypatch):
    """MCP 地址是部署配置而非密钥，允许由控制面注入一次。"""
    monkeypatch.setenv("BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/")
    bkn.configure_runtime({"token": "tok"})
    assert bkn.available() is True
    assert bkn._mcp_url().endswith("/mcp/")


def test_mcp_url_always_ends_with_slash(monkeypatch):
    """缺尾斜杠时网关 307 跳转，而 urllib 不对 POST 跟随重定向——症状是一个没有
    报文的 400，极难排查。所以在入口补齐。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc:1/api/x/v1/mcp"})
    assert bkn._mcp_url() == "http://svc:1/api/x/v1/mcp/"


def test_capability_surface_is_lazily_assembled(fake_toolkit, monkeypatch):
    """纯计算函数不该为 BKN 付代价：只导入不使用时不该有任何取工具包的动作。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert fake_toolkit == []           # 还没碰过任何能力

    assert bkn.whoami()["token"] == "tok"
    assert len(fake_toolkit) == 1


def test_runtime_is_reset_between_executions(fake_toolkit, monkeypatch):
    """会话池化复用，上一次执行的凭据不能留在进程里。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "first", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "first"

    bkn.configure_runtime({"token": "second", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "second"


def test_toolkit_is_cached_by_version(fake_toolkit, monkeypatch, tmp_path):
    """按 version（内容哈希）缓存：同版本不重复写盘，不同版本互不覆盖。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    bkn.whoami()

    cached = list((tmp_path / ".bkn-toolkit").glob("*.py"))
    assert len(cached) == 1
    assert cached[0].read_text(encoding="utf-8") == FAKE_STUB
    assert "sha256-fake" in cached[0].name      # 冒号被归一化，不能逃出目录


def test_unwritable_cache_does_not_break_the_call(fake_toolkit, monkeypatch):
    """/workspace 只读或写满都不该连累这次调用——缓存是优化，不是依赖。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    monkeypatch.setattr(bkn, "_CACHE_DIR", Path("/proc/definitely-not-writable"))
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "tok"


def test_dispatch_hands_the_event_to_the_capability_surface(fake_toolkit, monkeypatch):
    """用户函数不写 event、不接 token —— dispatch 已经替它配置好了。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)

    @sandbox_sdk.tool
    def peek(kn_id: str) -> dict:
        "看看能力面拿到了什么。"
        return {"kn_id": kn_id, "seen": bkn.whoami()}

    result = sandbox_sdk.dispatch({
        "kn_id": "worldcup",
        "token": "tok",
        "mcp": "http://svc/mcp/",
        "bkn": {"conversation_id": "conv_1", "interaction_id": "int_1"},
    })
    assert result["kn_id"] == "worldcup"
    # 生命周期上下文要原样传下去，脚本内的调用才挂得到同一次交互上。
    assert result["seen"]["bkn"]["conversation_id"] == "conv_1"
    assert result["seen"]["token"] == "tok"
    # 而 token / mcp 不该被当成业务入参喂给用户函数（它的签名里没有）。
    assert "token" not in result


def test_toolkit_version_is_recorded_by_the_loader(fake_toolkit, monkeypatch, tmp_path):
    """版本由加载器记，不从 stub 里读——stub 是渲染产物，不含自身版本。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert bkn.toolkit_version() is None        # 未装配
    bkn.whoami()
    assert bkn.toolkit_version() == "sha256:fake"
    bkn.configure_runtime({})                   # 换一次执行即重置
    assert bkn.toolkit_version() is None


def test_missing_stub_field_reports_clearly(monkeypatch, tmp_path):
    monkeypatch.setattr(bkn, "_fetch_toolkit", lambda: {"version": "v1"})
    monkeypatch.setattr(bkn, "_CACHE_DIR", tmp_path)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn.whoami()
    assert "stub" in str(excinfo.value)
