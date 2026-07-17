import asyncio
import json
from typing import Any, Optional

from langchain_core.messages import AIMessage
from langgraph.prebuilt import create_react_agent

from app import dao, observability
from app.config import config
from app.core.llm import build_chat_model
from app.core.structured import structured_extract
from app.core.prompt import resolve_prompt
from app.core.skills import load_skills
from app.db import SessionLocal
from app.errors import err
from app.models import AgentOut

MAX_AGENT_DEPTH = 3

# 进程内后台任务引用，防 GC；崩溃恢复语义（重启后 pending/running 任务标 failed）随 M6 落定
_background: set[asyncio.Task] = set()


async def run_agent_once(
    agent: AgentOut,
    message: str,
    prompt_vars: dict[str, Any],
    skills: list[str],
    prompt_override: Optional[str],
    account_id: str,
    account_type: str,
    depth: int,
    response_format: Optional[dict[str, Any]] = None,
) -> str:
    """一次性无状态执行：单次 graph run，无 checkpointer。被 /run 与 agent-as-tool 共用。

    response_format（JSON Schema）非空时走结构化输出：工具循环后再做一次结构化调用，
    返回序列化后的 JSON 字符串；否则返回最后一条 AI 文本回复。
    """
    if depth > MAX_AGENT_DEPTH:
        raise err(
            409,
            "Task.DepthExceeded",
            "agent 互调层级超限",
            f"执行栈深度超过 {MAX_AGENT_DEPTH}（疑似循环互调）",
            "检查 agent-as-tool 引用链是否成环。",
        )

    from app.core.tools import apply_tool_call_cap, load_tools  # 延迟导入破 tools↔runner 环

    async with SessionLocal() as session:
        system_prompt, prompt_source, prompt_version = await resolve_prompt(
            session, agent, account_id, prompt_override, prompt_vars
        )
    skill_ids = list(dict.fromkeys([*agent.skills, *skills]))
    system_prompt += await load_skills(skill_ids, account_id, account_type)
    tools = await load_tools(agent.tools, account_id, account_type, depth=depth)
    limits = agent.limits
    max_turns = limits.max_turns if limits and limits.max_turns else config.DEFAULT_MAX_TURNS
    timeout_s = limits.timeout_s if limits and limits.timeout_s else config.DEFAULT_TIMEOUT_S
    max_out = limits.max_output_tokens if limits else None
    model = build_chat_model(agent.model, max_output_tokens=max_out)
    tools = apply_tool_call_cap(tools, limits.max_tool_calls if limits else None)

    with observability.span(
        "agent.task",
        {
            "agent.id": agent.agent_id,
            "agent.name": agent.name,
            "task.depth": depth,
            "prompt.source": prompt_source,
            "prompt.version": prompt_version,
        },
    ):
        graph = create_react_agent(model, tools, prompt=system_prompt)
        async with asyncio.timeout(timeout_s):
            result = await graph.ainvoke(
                {"messages": [("user", message)]},
                {"recursion_limit": max_turns * 2 + 1},
            )
            if response_format:
                # 工具循环跑完后单独抽结构化：原生优先，模型不支持则提示词降级
                struct_model = build_chat_model(agent.model, streaming=False, max_output_tokens=max_out)
                obj = await structured_extract(struct_model, result["messages"], response_format)
                return json.dumps(obj, ensure_ascii=False)
    for msg in reversed(result["messages"]):
        if isinstance(msg, AIMessage) and msg.content:
            return msg.content if isinstance(msg.content, str) else str(msg.content)
    raise RuntimeError("graph 结束但没有产出 AI 回复")


async def execute_task(task_id: str, agent: AgentOut, req_input: dict, account_id: str, account_type: str) -> None:
    """执行到终态并落库（succeeded 必须等于结果可用）。/run 后台跑，/invoke 同步等。"""
    async with SessionLocal() as session:
        await dao.set_task_status(session, task_id, "running")
    try:
        output = await run_agent_once(
            agent,
            req_input["message"],
            req_input.get("prompt_vars") or {},
            req_input.get("skills") or [],
            req_input.get("prompt_override"),
            account_id,
            account_type,
            depth=1,
            response_format=req_input.get("response_format"),
        )
        async with SessionLocal() as session:
            # succeeded 必须等于结果可用（vega build-task 教训）
            await dao.set_task_status(session, task_id, "succeeded", output=output)
    except Exception as e:  # 失败必须落 failure_detail，不静默吞错
        detail = getattr(e, "detail", None)
        detail_text = str(detail) if detail else f"{type(e).__name__}: {e}"
        async with SessionLocal() as session:
            await dao.set_task_status(session, task_id, "failed", failure_detail=detail_text)


def submit_task(task_id: str, agent: AgentOut, req_input: dict, account_id: str, account_type: str) -> None:
    task = asyncio.create_task(execute_task(task_id, agent, req_input, account_id, account_type))
    _background.add(task)
    task.add_done_callback(_background.discard)
