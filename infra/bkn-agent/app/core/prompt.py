from typing import Any, Optional

from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.errors import bad_request, err
from app.models import AgentOut


class _StrictDict(dict):
    def __missing__(self, key):
        raise KeyError(key)


def _fill(template: str, prompt_vars: dict[str, Any], vars_schema: Optional[dict]) -> str:
    required = set((vars_schema or {}).get("required", []))
    missing = required - prompt_vars.keys()
    if missing:
        raise bad_request(
            "PromptVars",
            "提示词变量缺失",
            f"缺少变量: {sorted(missing)}",
            "按 prompt_vars_schema 补齐 prompt_vars。",
        )
    try:
        return template.format_map(_StrictDict(prompt_vars))
    except KeyError as e:
        raise bad_request("PromptVars", "提示词变量缺失", f"模板引用了未提供的变量 {e}")


async def resolve_prompt(
    session: AsyncSession,
    agent: AgentOut,
    account_id: str,
    request_override: Optional[str],
    prompt_vars: dict[str, Any],
) -> tuple[str, str, Optional[int]]:
    """三层解析：请求级 > 调用方级覆写 > agent 默认版本。三层共用 vars_schema。
    prompt_id 失效必须报明确错误，不回退内置默认词。
    返回 (正文, 来源层级, 默认层版本号)。"""
    schema = agent.prompt_vars_schema
    if request_override:
        return _fill(request_override, prompt_vars, schema), "request", None

    override = await dao.get_prompt_override(session, agent.agent_id, account_id)
    if override is not None:
        return _fill(override, prompt_vars, schema), "override", None

    if not agent.prompt_id:
        raise err(
            409,
            "Prompt.Unbound",
            "agent 未绑定提示词",
            f"agent {agent.agent_id} 无 prompt_id 且本次调用未提供覆写",
            "为 agent 绑定 prompt_id，或在请求中携带 prompt_override。",
        )
    default = await dao.get_default_prompt(session, agent.prompt_id)
    if default is None:
        raise err(
            409,
            "Prompt.Missing",
            "提示词不存在",
            f"prompt {agent.prompt_id} 或其当前版本不存在",
            "检查提示词是否被删除；不会回退到内置默认词。",
        )
    content, version_schema, version = default
    return _fill(content, prompt_vars, version_schema or schema), "default", version
