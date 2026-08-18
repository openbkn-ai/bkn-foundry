from typing import Any, Optional

from sqlalchemy.ext.asyncio import AsyncSession

from app import dao
from app.errors import bad_request, err
from app.models import AgentOut


class _StrictDict(dict):
    def __missing__(self, key):
        raise KeyError(key)


def _fill(template: str, prompt_vars: dict[str, Any], vars_schema: Optional[dict]) -> str:
    """Render the template only when variables are declared (vars_schema) or
    prompt_vars were supplied for this call.

    Otherwise return it verbatim. Writing a JSON output example such as
    `{"answer": ...}` inside a prompt is normal, and running format_map
    unconditionally would read those braces as variables: a KeyError turns into
    a misleading "missing variable" 400, and a stray `}` raises ValueError and
    surfaces as a 500. A prompt that declares nothing should reach the model
    exactly as written.
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
    except (ValueError, IndexError) as e:  # Unbalanced braces, positional fields, and similar template syntax errors.
        raise bad_request("BknAgent.Prompt.TemplateSyntax", error=str(e))


async def resolve_prompt(
    session: AsyncSession,
    agent: AgentOut,
    account_id: str,
    request_override: Optional[str],
    prompt_vars: dict[str, Any],
) -> tuple[str, str, Optional[int]]:
    """Resolve across three layers: request level > caller override > the agent
    default version. All three share one vars_schema. An invalid prompt_id must
    raise an explicit error instead of falling back to a built-in default.
    Returns (body, source layer, version of the default layer)."""
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
