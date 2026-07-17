"""预设 id：创建 agent/prompt 可指定 id（跨环境稳定引用），非法字符拒绝。"""
import pytest
from pydantic import ValidationError

from app.models import AgentSpec, PromptSpec


def test_agent_spec_accepts_preset_id():
    s = AgentSpec(agent_id="schema_translator.v1", name="t", mode="chat")
    assert s.agent_id == "schema_translator.v1"


def test_agent_spec_id_optional():
    assert AgentSpec(name="t", mode="chat").agent_id is None


def test_agent_spec_rejects_bad_id():
    for bad in ("has space", "斜杠/x", "a" * 51, ""):
        with pytest.raises(ValidationError):
            AgentSpec(agent_id=bad, name="t", mode="chat")


def test_prompt_spec_preset_id():
    assert PromptSpec(prompt_id="greeter-1", name="p", content="x").prompt_id == "greeter-1"
    assert PromptSpec(name="p", content="x").prompt_id is None
    with pytest.raises(ValidationError):
        PromptSpec(prompt_id="bad id", name="p", content="x")
