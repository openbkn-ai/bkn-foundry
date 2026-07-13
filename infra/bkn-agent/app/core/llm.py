from langchain_openai import ChatOpenAI

from app.config import config


def build_chat_model(agent_model: str) -> ChatOpenAI:
    """模型一律经 mf-model-api（集群内 /api/private）。model 为空 → 系统默认模型，
    由 mf-model-api 侧解析，这里不钉模型名。"""
    return ChatOpenAI(
        base_url=config.MF_MODEL_API_PRIVATE_BASE,
        api_key="internal",
        model=agent_model or config.DEFAULT_MODEL,
        streaming=True,
    )
