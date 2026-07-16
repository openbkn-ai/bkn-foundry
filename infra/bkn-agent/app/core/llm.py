from typing import Any, Optional

from langchain_openai import ChatOpenAI

from app.config import config


def normalize_response_format(rf: Optional[dict[str, Any]]) -> Optional[dict[str, Any]]:
    """create_react_agent 的 response_format 底层过 convert_to_openai_function：纯 JSON Schema
    若缺 name/title 会被判 `Unsupported function`。缺就补个 title，调用方可直接传裸 schema。"""
    if isinstance(rf, dict) and "title" not in rf and "name" not in rf:
        return {"title": "StructuredResponse", **rf}
    return rf


def build_chat_model(agent_model: str, streaming: bool = True) -> ChatOpenAI:
    """模型一律经 mf-model-api（集群内 /api/private）。model 为空 → 系统默认模型，
    由 mf-model-api 侧解析，这里不钉模型名。

    streaming=False 用于结构化输出：结构化调用是一次性取整个对象，且部分网关在流式
    结构化时会吐 choices=None 的 chunk 触发 langchain_openai 崩溃，非流式绕开。"""
    return ChatOpenAI(
        base_url=config.MF_MODEL_API_PRIVATE_BASE,
        api_key="internal",
        model=agent_model or config.DEFAULT_MODEL,
        streaming=streaming,
    )
