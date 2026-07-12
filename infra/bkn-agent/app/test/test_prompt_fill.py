import pytest
from fastapi import HTTPException

from app.core.prompt import _fill


def test_fill_ok():
    assert _fill("你是{role}", {"role": "客服"}, None) == "你是客服"


def test_fill_missing_required_var():
    with pytest.raises(HTTPException) as e:
        _fill("你是{role}", {}, {"required": ["role"]})
    assert e.value.detail["code"] == "BknAgent.ParamError.PromptVars"


def test_fill_template_references_unprovided_var():
    with pytest.raises(HTTPException) as e:
        _fill("你是{role}", {}, None)
    assert "role" in e.value.detail["detail"]


def test_fill_ignores_extra_vars():
    assert _fill("固定词", {"unused": 1}, None) == "固定词"
