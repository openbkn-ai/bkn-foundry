from typing import Any, Optional

from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.errors import bad_request, err
from app.models import AgentOut


class _StrictDict(dict):
    def __missing__(self, key):
        raise KeyError(key)


def _fill(template: str, prompt_vars: dict[str, Any], vars_schema: Optional[dict]) -> str:
    """只有声明了变量（vars_schema）或本次传了 prompt_vars 才做模板渲染。

    否则原样返回：提示词里写 JSON 输出示例（`{"answer": ...}`）是常态，无条件跑
    format_map 会把大括号当变量——KeyError 变成误导性的「变量缺失」400，落单的
    `}` 直接 ValueError 冒成 500。不渲染的提示词就该原样喂给模型。
    """
    schema = vars_schema or {}
    declared = bool(schema.get("required") or schema.get("properties"))
    if not declared and not prompt_vars:
        return template

    required = set(schema.get("required", []))
    missing = required - prompt_vars.keys()
    if missing:
        raise bad_request("BknAgent.Prompt.VarsMissing", variables=sorted(missing))
    try:
        return template.format_map(_StrictDict(prompt_vars))
    except KeyError as e:
        raise bad_request("BknAgent.Prompt.VarsUndeclared", variable=e)
    except (ValueError, IndexError) as e:  # 大括号不成对/位置参数等模板语法错
        raise bad_request("BknAgent.Prompt.TemplateSyntax", error=str(e))


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
        raise err(409, "BknAgent.Prompt.Unbound", agent_id=agent.agent_id)
    default = await dao.get_default_prompt(session, agent.prompt_id)
    if default is None:
        raise err(409, "BknAgent.Prompt.Missing", prompt_id=agent.prompt_id)
    content, version_schema, version = default
    return _fill(content, prompt_vars, version_schema or schema), "default", version
