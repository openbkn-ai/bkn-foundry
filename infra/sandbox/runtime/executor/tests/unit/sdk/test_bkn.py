"""sandbox_sdk.bkn 的单元测试。

验证的是「凭据从哪来、什么时候装配、版本怎么报」这些契约，不是 BKN 那些函数本身
——它们由 cmd/ptc-stub 从 MCP 工具目录渲染，正确性归 context-loader 那边的测试管。
"""

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[4]))

import sandbox_sdk                      # noqa: E402
from sandbox_sdk import bkn             # noqa: E402


@pytest.fixture(autouse=True)
def reset():
    bkn.configure_runtime({})
    yield
    bkn.configure_runtime({})


@pytest.fixture
def fake_surface(monkeypatch):
    """替掉能力面本体，只留装配链路。

    真产物有 25 个函数、35K 字符，测装配链路用不上它；而且拿它做断言，
    schema 一改测试就跟着碎。
    """
    import types

    calls = []
    module = types.ModuleType("fake_bkn_tools")
    module.__toolkit_version__ = "sha256:fake"
    module._CFG = {}

    def _configure(event):
        calls.append(event)
        module._CFG = dict(event)

    module._configure = _configure
    module.whoami = lambda: dict(module._CFG)

    monkeypatch.setattr(bkn, "_load_capability_module", lambda: module)
    return calls


def test_credentials_never_come_from_environment(monkeypatch):
    """令牌只认 event。

    沙箱会话池化复用，环境变量会把上一个调用方的值留在容器里。task_id 那类追踪
    标记漏了无伤大雅（Context.from_env 正是这么做的），令牌漏了是凭据泄露。
    所以这里必须报错，而不是回退到 env。
    """
    monkeypatch.setenv(
        "BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/"
    )
    monkeypatch.setenv("token", "leaked-from-previous-caller")
    monkeypatch.setenv("BKN_TOKEN", "leaked-from-previous-caller")

    bkn.configure_runtime({})           # event 里没有 token
    assert bkn.available() is False
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn._token()
    assert "环境变量" in str(excinfo.value)


def test_mcp_url_may_fall_back_to_deployment_env(monkeypatch):
    """MCP 地址是部署配置而非密钥，允许控制面注入一次。

    定义虽然烤进了镜像，调用仍要回访 MCP —— 地址少不了。
    """
    monkeypatch.setenv(
        "BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/"
    )
    bkn.configure_runtime({"token": "tok"})
    assert bkn.available() is True
    assert bkn._mcp_url().endswith("/mcp/")


def test_mcp_url_always_ends_with_slash(monkeypatch):
    """缺尾斜杠时网关 307 跳转，而 urllib 不对 POST 跟随重定向——症状是一个没有
    报文的 400，极难排查。所以在入口补齐。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc:1/api/x/v1/mcp"})
    assert bkn._mcp_url() == "http://svc:1/api/x/v1/mcp/"


def test_capability_surface_is_lazily_assembled(fake_surface, monkeypatch):
    """纯计算函数不该为 BKN 付代价：只导入不使用时不该装配。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert fake_surface == []           # 还没碰过任何能力

    assert bkn.whoami()["token"] == "tok"
    assert len(fake_surface) == 1


def test_runtime_is_reset_between_executions(fake_surface, monkeypatch):
    """能力面模块是进程级单例，而沙箱会话池化复用——上一次执行的凭据不能留下。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "first", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "first"

    bkn.configure_runtime({"token": "second", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "second"
    # 换一次执行必须重新 _configure，而不是复用上一次配好的模块。
    assert len(fake_surface) == 2


def test_dispatch_hands_the_event_to_the_capability_surface(fake_surface, monkeypatch):
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
    # token / mcp 不该被当成业务入参喂给用户函数（它的签名里没有）。
    assert "token" not in result


def test_toolkit_version_comes_from_the_built_artifact(fake_surface, monkeypatch):
    """版本是构建期写进产物的，不是运行时算的。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert bkn.toolkit_version() is None         # 未装配
    bkn.whoami()
    assert bkn.toolkit_version() == "sha256:fake"
    bkn.configure_runtime({})                    # 换一次执行即重置
    assert bkn.toolkit_version() is None


def test_missing_artifact_points_at_the_build_step(monkeypatch):
    """产物缺失是构建事故，报错要直接给出重新生成的命令。"""
    def _boom():
        raise ImportError("no module named _bkn_tools")

    monkeypatch.setattr(bkn, "_load_capability_module", _boom)
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn.whoami()
    assert "make -C infra/sandbox bkn-tools" in str(excinfo.value)


def test_version_check_compares_image_against_server(fake_surface, monkeypatch):
    """镜像滞后于服务端时症状是签名对不上，而错误里看不出根因——留个能直接问的口子。"""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})

    monkeypatch.setattr(bkn, "_fetch_toolkit", lambda: {"version": "sha256:fake"})
    assert bkn.check_against_server()["in_sync"] is True

    monkeypatch.setattr(bkn, "_fetch_toolkit", lambda: {"version": "sha256:newer"})
    result = bkn.check_against_server()
    assert result["in_sync"] is False
    assert result["local"] == "sha256:fake"
    assert result["remote"] == "sha256:newer"


def test_shipped_artifact_is_importable_and_versioned():
    """镜像里那份产物本身要能 import，且带着构建期写入的版本。

    上面的用例都把能力面替掉了，这里是唯一碰真产物的地方——它若坏了，
    整条离线路径就是坏的。
    """
    from sandbox_sdk import _bkn_tools

    assert _bkn_tools.__toolkit_version__.startswith("sha256:")
    assert callable(_bkn_tools._configure)
    # 抽查几个能力：签名清单变了不该悄悄少函数。
    for name in ("list_knowledge_networks", "query_object_instance",
                 "run_sql", "list_resources"):
        assert callable(getattr(_bkn_tools, name)), name
