"""结构化输出：原生优先 + 提示词强制 JSON 降级。

对话/一次性的工具循环跑完后，从会话消息里抽出符合 JSON Schema 的对象：
1. 原生：model.with_structured_output(schema)（解码级约束，最强，需模型支持）。
2. 降级：原生报错（模型不支持，如思考模式 qwen 拒 tool_choice=required）时，
   拼 schema 进提示词让模型只输出 JSON，jsonschema 校验，不合法喂回重试一次。
降级不保证一定成，但对任何能对话的模型都可用。
"""
import json
import logging
import re
from typing import Any

from jsonschema import validate as _jsonschema_validate
from jsonschema.exceptions import SchemaError, ValidationError
from langchain_core.messages import AIMessage, SystemMessage

from app.core.llm import normalize_response_format

logger = logging.getLogger(__name__)

_FENCE = re.compile(r"^\s*```(?:json)?\s*|\s*```\s*$", re.IGNORECASE)


def _extract_json(text: str) -> Any:
    """从模型文本里抠 JSON：剥 markdown 围栏，再取首个 { 到末个 } 之间。"""
    t = _FENCE.sub("", text.strip())
    start, end = t.find("{"), t.rfind("}")
    if start != -1 and end != -1 and end > start:
        t = t[start : end + 1]
    return json.loads(t)


def _with_system_prompt(messages: list, system_prompt: str | None) -> list:
    """把 agent 的系统提示词补回抽取调用的消息头。

    langchain 1.x 的 create_agent(system_prompt=...) 只在模型请求时注入，不落进
    graph state——实测 result["messages"] 只有 [HumanMessage, AIMessage]。抽取是
    另起的一次模型调用，若不补，它看到的就只有「原始输入 + 上一轮回答」，agent
    的领域约束一条都不在场。对语义理解这类任务，最省力的填法就是把输入里的技术
    字段名原样抄进 display_name（#556）。

    state 里已带 SystemMessage 时不重复补（未来 langgraph 若改行为，这里自适应）。
    """
    if not system_prompt:
        return list(messages)
    if messages and isinstance(messages[0], SystemMessage):
        return list(messages)
    return [SystemMessage(content=system_prompt), *messages]


async def structured_extract(
    model, messages: list, schema: dict, system_prompt: str | None = None
) -> dict:
    """从 messages 抽出符合 schema 的对象。model 应为非流式（见 build_chat_model）。"""
    obj, _ = await structured_extract_with_path(model, messages, schema, system_prompt)
    return obj


async def structured_extract_with_path(
    model, messages: list, schema: dict, system_prompt: str | None = None
) -> tuple[dict, str]:
    """返回结构化对象及 validation path：native 或 fallback。

    system_prompt 为 agent 本轮生效的系统提示词（含技能段），抽取调用必须带上，
    否则模型在无约束状态下填 schema，见 _with_system_prompt。
    """
    # 对话 Agent 通常已经按系统提示词在最后一条消息中给出目标 JSON。
    # 先复用并校验这份结果，避免为了相同内容再调用一次模型；仅当它不符合
    # schema 时，才进入原生结构化与提示词降级路径。
    for message in reversed(messages):
        if not isinstance(message, AIMessage) or not isinstance(message.content, str):
            continue
        try:
            obj = _extract_json(message.content)
            _jsonschema_validate(obj, schema)
            logger.warning("[Structured] path=conversation system_prompt=%s", bool(system_prompt))
            return obj, "conversation"
        except (json.JSONDecodeError, ValidationError, ValueError, TypeError):
            break

    messages = _with_system_prompt(messages, system_prompt)
    # 1. 原生
    try:
        norm = normalize_response_format(schema)
        r = await model.with_structured_output(norm).ainvoke(messages)
        obj = r.model_dump() if hasattr(r, "model_dump") else dict(r)
        # 原生也校验：with_structured_output 未启 strict，可能缺 required/类型不符；
        # 不合法则不当成功返回，落到下面提示词降级重试。
        _jsonschema_validate(obj, schema)
        # path 只进 bkn-trace evidence 的话，trace 摄取一坏（曾遇 503
        # INGEST_AUTH_NOT_CONFIGURED）就彻底查不到走的哪条路。本地留一行：
        # 排「结构化结果质量不对」时，先要知道是原生还是降级、提示词在不在场。
        # 用 warning 而非 info：main.py 直接 uvicorn.run，没配 log_config，
        # 全仓也没有 basicConfig/dictConfig——root 停在 WARNING 且 handlers 为空，
        # 应用侧 logger.info 会被整条丢弃（Pod 实测 3000 行日志里 [Toolbox] 零命中）。
        # 每次带 response_format 的调用才一条，量可控。
        logger.warning(
            "[Structured] path=native system_prompt=%s", bool(system_prompt)
        )
        return obj, "native"
    except SchemaError:
        # schema 本体非法：请求边界已用 check_schema 拦（models.py ResponseFormat），
        # 这里兜底。降级路径同样必炸，直接抛出，不白费模型调用。
        raise
    except Exception as e:  # 模型不支持结构化或原生结果不合法 → 降级
        logger.warning("[Structured] 原生结构化失败/不合法，降级到提示词模式：%s", e)

    # 2. 提示词强制 JSON + 校验 + 重试一次
    # 「本次调用不提供任何工具」是必要的一句：system_prompt 里可能含技能段，
    # 而 load_skills 注入的正文固定写着「需要时调用 read_skill_file 按需读取」。
    # 抽取用的是没 bind 任何工具的裸模型，模型若照着去要工具就会回一句自然语言
    # 而不是 JSON，白烧一次重试；两次都这样整个任务 failed。
    instr = (
        "请只输出一个 JSON 对象，严格符合下面的 JSON Schema。"
        "本次调用不提供任何工具，请仅基于以上对话内容作答，不要请求调用工具。"
        "不要 markdown 代码块，不要任何多余文字或解释：\n"
        + json.dumps(schema, ensure_ascii=False)
    )
    msgs = list(messages) + [("user", instr)]
    last_err: Any = None
    for attempt in range(1, 3):
        resp = await model.ainvoke(msgs)
        text = resp.content if isinstance(resp.content, str) else str(resp.content)
        try:
            obj = _extract_json(text)
            _jsonschema_validate(obj, schema)
            logger.warning(
                "[Structured] path=fallback attempt=%d system_prompt=%s",
                attempt,
                bool(system_prompt),
            )
            return obj, "fallback"
        except (json.JSONDecodeError, ValidationError, ValueError) as e:
            last_err = e
            msgs = msgs + [
                ("assistant", text),
                ("user", f"上面的输出不合法（{e}）。请重新只输出严格符合 schema 的 JSON。"),
            ]
    raise RuntimeError(f"结构化输出失败：原生不支持且提示词降级仍不合法（{last_err}）")
