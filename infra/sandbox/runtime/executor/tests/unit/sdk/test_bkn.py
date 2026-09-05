"""Unit tests for sandbox_sdk.bkn.

These tests validate contracts such as where credentials come from, when runtime wiring happens, and how the version is reported; they do not test the BKN functions themselves.
Those functions are rendered by cmd/ptc-stub from the MCP tool directory, and their correctness is covered by context-loader tests.
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
    """Replace the capability surface itself and keep only the wiring path.

    The real artifact has 25 functions and 35K characters, which is unnecessary for wiring tests; asserting against it would also
    make these tests break whenever the schema changes.
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


def test_credentials_come_from_process_env(monkeypatch):
    """Default path: user code has no token, and the execution factory injects it as a process-level environment variable."""
    monkeypatch.setenv(
        "BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/"
    )
    monkeypatch.setenv("BKN_TOKEN", "tok-from-env")

    bkn.configure_runtime({})           # there is nothing in event
    assert bkn.available() is True
    assert bkn._token() == "tok-from-env"


def test_event_credentials_win_over_environment(monkeypatch):
    """event takes precedence over env.

    Execution request env is assembled per run and disappears with the process, but env_vars provided when creating the session are container-level
    and can persist across callers. If a pooled container keeps an old token, the value explicitly provided by the caller must win.
    """
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    monkeypatch.setenv("BKN_TOKEN", "stale-from-pooled-container")

    bkn.configure_runtime({"token": "fresh-from-caller", "mcp": "http://svc/mcp/"})
    assert bkn._token() == "fresh-from-caller"


def test_missing_credentials_report_both_ways_in(monkeypatch):
    monkeypatch.setenv(
        "BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/"
    )
    monkeypatch.delenv("BKN_TOKEN", raising=False)

    bkn.configure_runtime({})
    assert bkn.available() is False
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn._token()
    assert "bkn_token" in str(excinfo.value)


def test_business_context_falls_back_to_process_env(monkeypatch, fake_surface):
    """Session context also does not need to be written into user code."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    monkeypatch.setenv("BKN_TOKEN", "tok")
    monkeypatch.setenv("BKN_CONVERSATION_ID", "conv_env")
    monkeypatch.setenv("BKN_INTERACTION_ID", "int_env")

    bkn.configure_runtime({"mcp": "http://svc/mcp/"})
    seen = bkn.whoami()["bkn"]
    assert seen == {"conversation_id": "conv_env", "interaction_id": "int_env"}

    # event takes precedence here as well.
    bkn.configure_runtime({
        "mcp": "http://svc/mcp/",
        "bkn": {"conversation_id": "conv_event", "interaction_id": "int_event"},
    })
    assert bkn.whoami()["bkn"]["conversation_id"] == "conv_event"


def test_mcp_url_may_fall_back_to_deployment_env(monkeypatch):
    """The MCP address is deployment configuration rather than a secret, so the control plane may inject it once.

    Although the definitions are baked into the image, calls still need to reach MCP, so the address is required.
    """
    monkeypatch.setenv(
        "BKN_SANDBOX_MCP_URL", "http://agent-retrieval:30779/api/x/v1/mcp/"
    )
    bkn.configure_runtime({"token": "tok"})
    assert bkn.available() is True
    assert bkn._mcp_url().endswith("/mcp/")


def test_mcp_url_always_ends_with_slash(monkeypatch):
    """Without the trailing slash, the gateway returns a 307 redirect and urllib does not follow redirects for POST; the symptom is a
    400 without a response body, which is very hard to diagnose. Normalize it at the entry point."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc:1/api/x/v1/mcp"})
    assert bkn._mcp_url() == "http://svc:1/api/x/v1/mcp/"


def test_capability_surface_is_lazily_assembled(fake_surface, monkeypatch):
    """Pure compute functions should not pay for BKN: importing without using it should not trigger wiring."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert fake_surface == []           # no capability has been touched yet

    assert bkn.whoami()["token"] == "tok"
    assert len(fake_surface) == 1


def test_runtime_is_reset_between_executions(fake_surface, monkeypatch):
    """The capability surface module is a process-level singleton and sandbox sessions are pooled, so credentials from the previous execution must not remain."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "first", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "first"

    bkn.configure_runtime({"token": "second", "mcp": "http://svc/mcp/"})
    assert bkn.whoami()["token"] == "second"
    # Each execution must call _configure again instead of reusing the module configured by the previous execution.
    assert len(fake_surface) == 2


def test_dispatch_hands_the_event_to_the_capability_surface(fake_surface, monkeypatch):
    """The user function neither writes event nor accepts token; dispatch has already configured these values for it."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)

    @sandbox_sdk.tool
    def peek(kn_id: str) -> dict:
        "Inspect what the capability surface receives."
        return {"kn_id": kn_id, "seen": bkn.whoami()}

    result = sandbox_sdk.dispatch({
        "kn_id": "worldcup",
        "token": "tok",
        "mcp": "http://svc/mcp/",
        "bkn": {"conversation_id": "conv_1", "interaction_id": "int_1"},
    })
    assert result["kn_id"] == "worldcup"
    # The lifecycle context must be passed through unchanged so in-script calls are attached to the same interaction.
    assert result["seen"]["bkn"]["conversation_id"] == "conv_1"
    assert result["seen"]["token"] == "tok"
    # token and mcp must not be passed to the user function as business arguments because they are not in its signature.
    assert "token" not in result


def test_toolkit_version_comes_from_the_built_artifact(fake_surface, monkeypatch):
    """The version is written into the artifact at build time rather than computed at runtime."""
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    assert bkn.toolkit_version() is None         # not wired
    bkn.whoami()
    assert bkn.toolkit_version() == "sha256:fake"
    bkn.configure_runtime({})                    # reset for each execution
    assert bkn.toolkit_version() is None


def test_missing_artifact_points_at_the_build_step(monkeypatch):
    """A missing artifact is a build issue, and the error should directly provide the regeneration command."""
    def _boom():
        raise ImportError("no module named _bkn_tools")

    monkeypatch.setattr(bkn, "_load_capability_module", _boom)
    monkeypatch.delenv("BKN_SANDBOX_MCP_URL", raising=False)
    bkn.configure_runtime({"token": "tok", "mcp": "http://svc/mcp/"})
    with pytest.raises(bkn.BKNNotConfigured) as excinfo:
        bkn.whoami()
    assert "make -C infra/sandbox bkn-tools" in str(excinfo.value)


def test_shipped_artifact_is_importable_and_versioned():
    """The artifact in the image must be importable and include the version written at build time.

    The tests above replace the capability surface; this is the only one that touches the real artifact, so if it is broken,
    the whole offline path is broken.
    """
    from sandbox_sdk import _bkn_tools

    assert _bkn_tools.__toolkit_version__.startswith("sha256:")
    assert callable(_bkn_tools._configure)
    # Spot-check several capabilities: changes to the signature manifest must not silently remove functions.
    for name in ("list_knowledge_networks", "query_object_instance",
                 "run_sql", "list_resources"):
        assert callable(getattr(_bkn_tools, name)), name


def test_internal_queries_carry_parent_without_reusing_operation_id(monkeypatch):
    from sandbox_sdk import _bkn_tools

    monkeypatch.setenv("BKN_TOKEN", "test-token")
    monkeypatch.setenv("BKN_SANDBOX_MCP_URL", "http://svc/mcp/")
    monkeypatch.setenv("BKN_CONVERSATION_ID", "conv_test")
    monkeypatch.setenv("BKN_INTERACTION_ID", "int_test")
    monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
    # Avoid _configure's working-directory setup; keep the real call serializer.
    def configure(event):
        monkeypatch.setattr(_bkn_tools, "_CFG", dict(event))

    monkeypatch.setattr(_bkn_tools, "_configure", configure)
    calls = []

    def rpc(method, params):
        calls.append(params)
        return {"result": {"content": [{"type": "text", "text": '{"datas": []}'}]}}

    monkeypatch.setattr(_bkn_tools, "_rpc", rpc)
    for parent in ("op_function_a", "op_function_b", ""):
        monkeypatch.setenv("BKN_PARENT_OPERATION_ID", parent)
        bkn.configure_runtime({})
        for ot in ("bom", "inventory"):
            bkn.query_object_instance(kn_id="kn_test", ot_id=ot)
            ctx = calls[-1]["arguments"]["bkn_context"]
            assert ctx.get("parent_operation_id", "") == parent
            assert ctx["interaction_id"] == "int_test"
            assert "operation_id" not in ctx
            assert "operation_key" not in ctx


def test_explicit_runtime_parent_wins(monkeypatch):
    monkeypatch.setenv("BKN_PARENT_OPERATION_ID", "op_env")
    bkn.configure_runtime({"bkn": {"parent_operation_id": "op_explicit"}})
    assert bkn._business_context()["parent_operation_id"] == "op_explicit"


def test_environment_parent_does_not_cross_explicit_turn(monkeypatch):
    monkeypatch.setenv("BKN_CONVERSATION_ID", "conv_env")
    monkeypatch.setenv("BKN_INTERACTION_ID", "int_env")
    monkeypatch.setenv("BKN_PARENT_OPERATION_ID", "op_env")
    bkn.configure_runtime({"bkn": {
        "conversation_id": "conv_other", "interaction_id": "int_other",
    }})
    assert bkn._business_context() == {
        "conversation_id": "conv_other", "interaction_id": "int_other",
    }
