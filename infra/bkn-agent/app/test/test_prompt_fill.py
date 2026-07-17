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
    """声明了变量（schema 有 properties）但模板引用了没给的变量 → 400。"""
    with pytest.raises(HTTPException) as e:
        _fill("你是{role}", {}, {"properties": {"other": {"type": "string"}}})
    assert "role" in e.value.detail["detail"]


def test_fill_ignores_extra_vars():
    assert _fill("固定词", {"unused": 1}, None) == "固定词"


def test_no_vars_declared_means_no_rendering():
    """P1 回归：不声明变量、不传变量的提示词原样返回。

    提示词里写 JSON 输出示例是常态；老实现无条件 format_map，
    `{"answer": ...}` 被当变量 → 误导性 400，落单 `}` → 裸 500。
    """
    tpl = '返回格式：{"answer": "...", "score": 1}'
    assert _fill(tpl, {}, None) == tpl
    assert _fill("结尾有个孤立大括号 }", {}, None) == "结尾有个孤立大括号 }"


def test_brace_syntax_error_is_400_not_500():
    """带变量的提示词里大括号不成对 → 明确 400（提示转义），不是 500。"""
    with pytest.raises(HTTPException) as e:
        _fill("你是{role}，格式 }", {"role": "客服"}, {"required": ["role"]})
    assert e.value.detail["code"] == "BknAgent.ParamError.PromptTemplate"
    assert e.value.status_code == 400
