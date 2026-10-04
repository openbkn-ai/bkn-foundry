"""
Unit tests for sandbox_sdk.

These call the SDK for real rather than asserting on generated source, so the
schema inference and event unpacking are actually exercised.
"""

import sys
from pathlib import Path
from typing import Dict, List, Optional

import pytest

# The SDK ships separately from the executor package
sys.path.insert(0, str(Path(__file__).resolve().parents[4]))

import sandbox_sdk  # noqa: E402
from sandbox_sdk import Context, tool  # noqa: E402
from sandbox_sdk import _bkn_tools  # noqa: E402


@pytest.fixture(autouse=True)
def clean_registry():
    """Each test registers its own entry."""
    sandbox_sdk._REGISTRY.clear()
    sandbox_sdk._ENTRY = None
    yield
    sandbox_sdk._REGISTRY.clear()
    sandbox_sdk._ENTRY = None


class TestSchemaInference:
    def test_basic_types(self):
        @tool
        def f(a: str, b: int, c: float, d: bool) -> dict:
            """doc"""
            return {}

        types = {p["name"]: p["type"] for p in f.inputs}
        assert types == {"a": "string", "b": "number", "c": "number", "d": "boolean"}
        assert f.schema()["description"] == "doc"

    def test_default_makes_optional(self):
        @tool
        def f(a: int, b: int = 5) -> int:
            return a + b

        by_name = {p["name"]: p for p in f.inputs}
        assert by_name["a"]["required"] is True
        assert by_name["b"]["required"] is False
        assert by_name["b"]["default"] == 5

    def test_optional_and_pep604_agree(self):
        """Optional[int] and `int | None` describe the same thing."""

        @tool
        def old(x: Optional[int]) -> int:
            return x or 0

        old_type = old.inputs[0]["type"]

        sandbox_sdk._REGISTRY.clear()
        sandbox_sdk._ENTRY = None

        ns = {}
        exec(
            "from sandbox_sdk import tool\n"
            "@tool\n"
            "def new(x: int | None) -> int:\n"
            "    return x or 0\n",
            ns,
        )
        new_type = ns["new"].inputs[0]["type"]

        assert old_type == "number"
        assert new_type == old_type

    def test_list_element_structure(self):
        @tool
        def f(items: List[str]) -> dict:
            return {}

        param = f.inputs[0]
        assert param["type"] == "array"
        assert param["sub_parameters"][0]["type"] == "string"

    def test_union_without_none_stays_required(self):
        """Union[int, str] says the type may vary, not that it is optional."""
        from typing import Union

        @tool
        def f(x: Union[int, str]) -> str:
            return str(x)

        assert f.inputs[0]["required"] is True
        with pytest.raises(ValueError, match="x"):
            sandbox_sdk.dispatch({})

    def test_varargs_are_not_parameters(self):
        @tool
        def f(a: int, *extra, **opts) -> int:
            return a

        assert [p["name"] for p in f.inputs] == ["a"]

    def test_parameter_named_args_is_kept(self):
        """A normal parameter that happens to be called args is a real input."""

        @tool
        def f(args: str) -> str:
            return args

        assert [p["name"] for p in f.inputs] == ["args"]

    def test_context_is_not_an_input(self):
        @tool
        def f(a: int, ctx: Context) -> dict:
            return {}

        assert [p["name"] for p in f.inputs] == ["a"]

    def test_single_entry_enforced(self):
        @tool
        def first(a: int) -> int:
            return a

        with pytest.raises(RuntimeError, match="只能有一个"):

            @tool
            def second(b: int) -> int:
                return b


class TestDispatch:
    def test_event_unpacks_into_parameters(self):
        @tool
        def add(a: int, b: int) -> int:
            return a + b

        assert sandbox_sdk.dispatch({"a": 2, "b": 3}) == 5

    def test_default_used_when_absent(self):
        @tool
        def f(a: int, b: int = 7) -> int:
            return a + b

        assert sandbox_sdk.dispatch({"a": 1}) == 8

    def test_missing_required_names_the_parameter(self):
        @tool
        def add(a: int, b: int) -> int:
            return a + b

        with pytest.raises(ValueError, match="b"):
            sandbox_sdk.dispatch({"a": 1})

    def test_varargs_do_not_block_dispatch(self):
        @tool
        def f(a: int, *extra, **opts) -> int:
            return a

        assert sandbox_sdk.dispatch({"a": 4}) == 4

    def test_unknown_entry_reports_registry(self):
        @tool
        def f(a: int) -> int:
            return a

        with pytest.raises(RuntimeError, match="未注册"):
            sandbox_sdk.dispatch({}, entry="nope")

    def test_context_injected_from_environment(self, monkeypatch):
        monkeypatch.setenv("user_name", "cx")
        monkeypatch.setenv("user_id", "u_1")

        @tool
        def who(greeting: str, ctx: Context) -> dict:
            return {"msg": greeting + ", " + ctx.user_name, "uid": ctx.user_id}

        assert sandbox_sdk.dispatch({"greeting": "hi"}) == {"msg": "hi, cx", "uid": "u_1"}


class TestBknToolsMcpDecoding:
    @pytest.fixture(autouse=True)
    def clean_trace_gaps(self):
        _bkn_tools._TRACE_GAPS.clear()
        yield
        _bkn_tools._TRACE_GAPS.clear()

    def test_trace_diagnostic_content_does_not_break_business_json(self, monkeypatch):
        """Regression fixture from the real Core-down run_cypher response."""
        business_text = (
            '{"columns":[{"name":"product_count","type":"integer"}],'
            '"entries":[{"product_count":30}]}'
        )
        diagnostic_text = (
            '[BKN_TRACE]{"bkn_trace":{"available":false,"recorded":false,'
            '"stage":"ensure_operation","code":"trace_core_unavailable",'
            '"required_action":"continue_business_without_trace_retry",'
            '"partial_reasons":["trace_call_unrecorded:run_cypher:'
            'req_92343e69-c464-49e9-86b8-a3cc2aba92d9"]}}'
        )
        response = {
            "result": {
                "content": [
                    {"type": "text", "text": business_text},
                    {"type": "text", "text": diagnostic_text},
                ]
            }
        }
        monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
        monkeypatch.setattr(_bkn_tools, "_rpc", lambda *args, **kwargs: response)

        assert _bkn_tools._call("run_cypher", {}) == {
            "columns": [{"name": "product_count", "type": "integer"}],
            "entries": [{"product_count": 30}],
        }

    def test_trace_diagnostic_preserves_bounded_partial_reason_marker(self, monkeypatch, capsys):
        diagnostic_text = (
            '[BKN_TRACE]{"bkn_trace":{"partial_reasons":['
            '"trace_call_unrecorded:run_cypher:req_1", "bad reason"]}}'
        )
        response = {
            "result": {
                "content": [
                    {"type": "text", "text": '{"ok":true}'},
                    {"type": "text", "text": diagnostic_text},
                ]
            }
        }
        monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
        monkeypatch.setattr(_bkn_tools, "_rpc", lambda *args, **kwargs: response)

        assert _bkn_tools._call("run_cypher", {}) == {"ok": True}
        marker = '[BKN_TRACE_GAP]{"partial_reason":"trace_call_unrecorded:run_cypher:req_1"}'
        assert marker in capsys.readouterr().err

    def test_trace_diagnostic_does_not_hide_business_error(self, monkeypatch):
        response = {
            "result": {
                "isError": True,
                "content": [
                    {"type": "text", "text": '{"error":"business failed"}'},
                    {"type": "text", "text": '[BKN_TRACE]{"bkn_trace":{"partial_reasons":[]}}'},
                ],
            }
        }
        monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
        monkeypatch.setattr(_bkn_tools, "_rpc", lambda *args, **kwargs: response)

        with pytest.raises(_bkn_tools.ToolError, match="business failed"):
            _bkn_tools._call("run_cypher", {})

    def test_trace_diagnostic_keeps_toon_business_text(self, monkeypatch):
        response = {
            "result": {
                "content": [
                    {"type": "text", "text": "product_count\t30\n"},
                    {"type": "text", "text": '[BKN_TRACE]{"bkn_trace":{}}'},
                ]
            }
        }
        monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
        monkeypatch.setattr(_bkn_tools, "_rpc", lambda *args, **kwargs: response)

        assert _bkn_tools._call("run_cypher", {}) == "product_count\t30\n"

    def test_malformed_trace_diagnostic_remains_business_text(self, monkeypatch):
        response = {
            "result": {
                "content": [
                    {"type": "text", "text": '{"ok":true}'},
                    {"type": "text", "text": "[BKN_TRACE]{bad}"},
                ]
            }
        }
        monkeypatch.setattr(_bkn_tools, "_ensure_session", lambda: None)
        monkeypatch.setattr(_bkn_tools, "_rpc", lambda *args, **kwargs: response)

        assert _bkn_tools._call("run_cypher", {}) == '{"ok":true}[BKN_TRACE]{bad}'

    def test_configure_starts_each_execution_with_fresh_trace_gap_state(
        self, tmp_path, monkeypatch
    ):
        _bkn_tools._TRACE_GAPS.add("trace_call_unrecorded:run_cypher:req_old")
        monkeypatch.chdir(tmp_path)
        monkeypatch.setattr(_bkn_tools.pathlib.Path, "mkdir", lambda *args, **kwargs: None)
        monkeypatch.setattr(_bkn_tools.os, "chdir", lambda *_args: None)

        _bkn_tools._configure({"bkn": {"conversation_id": "conv_new"}})

        assert _bkn_tools._TRACE_GAPS == set()


pydantic = pytest.importorskip("pydantic")


class TestPydantic:
    def test_nested_model_expands(self):
        class Address(pydantic.BaseModel):
            city: str
            zipcode: str = ""

        class Profile(pydantic.BaseModel):
            name: str
            address: Address

        @tool
        def reg(profile: Profile) -> dict:
            return {}

        param = reg.inputs[0]
        assert param["type"] == "object"
        subs = {p["name"]: p for p in param["sub_parameters"]}
        assert subs["name"]["required"] is True
        assert subs["address"]["type"] == "object"
        assert subs["address"]["sub_parameters"][0]["name"] == "city"

    def test_optional_field_keeps_its_type(self):
        """pydantic v2 renders Optional[int] as anyOf with no top-level type."""

        class M(pydantic.BaseModel):
            count: Optional[int] = None

        @tool
        def f(m: M) -> dict:
            return {}

        sub = f.inputs[0]["sub_parameters"][0]
        assert sub["type"] == "number"

    def test_self_referencing_model_terminates(self):
        class Node(pydantic.BaseModel):
            name: str
            child: Optional["Node"] = None

        Node.model_rebuild()

        @tool
        def f(node: Node) -> dict:
            return {}

        assert f.inputs[0]["type"] == "object"

    def test_dispatch_builds_model_and_validates(self):
        class P(pydantic.BaseModel):
            name: str
            age: int

        @tool
        def reg(p: P) -> dict:
            return {"n": p.name, "a": p.age}

        assert sandbox_sdk.dispatch({"p": {"name": "cx", "age": 30}}) == {"n": "cx", "a": 30}

        with pytest.raises(pydantic.ValidationError):
            sandbox_sdk.dispatch({"p": {"name": "cx", "age": "not a number"}})
