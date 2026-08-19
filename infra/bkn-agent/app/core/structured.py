"""Structured output: native first, with a prompt-forced JSON fallback.

Once the chat or one-shot tool loop has finished, extract an object matching the
JSON Schema from the conversation messages:
1. Native: model.with_structured_output(schema). This is a decoding-level
   constraint, the strongest option, and requires model support.
2. Fallback: when the native path errors because the model does not support it
   — a thinking-mode qwen rejects tool_choice=required, for instance — the
   schema is folded into the prompt so the model emits JSON only, the result is
   validated with jsonschema, and an invalid one is fed back for a single retry.
The fallback is not guaranteed to succeed, but it works with any model that can
hold a conversation.
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
    """Dig the JSON out of model text: strip the markdown fence, then take
    everything between the first { and the last }."""
    t = _FENCE.sub("", text.strip())
    start, end = t.find("{"), t.rfind("}")
    if start != -1 and end != -1 and end > start:
        t = t[start : end + 1]
    return json.loads(t)


def _with_system_prompt(messages: list, system_prompt: str | None) -> list:
    """Put the agent system prompt back at the head of the extraction messages.

    create_agent(system_prompt=...) in langchain 1.x injects the prompt only when
    it calls the model; it never lands in graph state, and in practice
    result["messages"] holds just [HumanMessage, AIMessage]. Extraction is a
    separate model call, so without this it would see only the original input
    plus the previous answer, with none of the agent's domain constraints
    present. For a task such as semantic understanding, the laziest way to fill
    the schema is then to copy the technical field names straight into
    display_name (#556).

    When the state already carries a SystemMessage nothing is prepended, so this
    adapts if langgraph changes that behaviour later.
    """
    if not system_prompt:
        return list(messages)
    if messages and isinstance(messages[0], SystemMessage):
        return list(messages)
    return [SystemMessage(content=system_prompt), *messages]


async def structured_extract(
    model, messages: list, schema: dict, system_prompt: str | None = None
) -> dict:
    """Extract an object matching the schema from messages. The model should be
    non-streaming; see build_chat_model."""
    obj, _ = await structured_extract_with_path(model, messages, schema, system_prompt)
    return obj


async def structured_extract_with_path(
    model, messages: list, schema: dict, system_prompt: str | None = None
) -> tuple[dict, str]:
    """Return the structured object together with its validation path, native or
    fallback.

    system_prompt is the system prompt in effect for this turn, skill sections
    included. The extraction call must carry it, otherwise the model fills the
    schema with no constraints at all; see _with_system_prompt.
    """
    # A chat agent has usually already produced the target JSON in its last
    # message, as the system prompt asked. Reuse and validate that result first
    # to avoid calling the model again for the same content; only when it does
    # not match the schema do the native and prompt-fallback paths run.
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
    # 1. Native
    try:
        norm = normalize_response_format(schema)
        r = await model.with_structured_output(norm).ainvoke(messages)
        obj = r.model_dump() if hasattr(r, "model_dump") else dict(r)
        # Validate the native result too: with_structured_output does not run in
        # strict mode, so a required field may be missing or a type may not
        # match. An invalid result is not returned as a success; it falls through
        # to the prompt fallback below.
        _jsonschema_validate(obj, schema)
        # If the path only reached bkn-trace evidence, one broken trace ingest
        # (a 503 INGEST_AUTH_NOT_CONFIGURED happened once) would make it
        # impossible to tell which path ran. Keep a local line: when
        # investigating "the structured result is poor", the first thing to know
        # is whether it came from the native or the fallback path, and whether
        # the prompt was present.
        # warning rather than info: main.py calls uvicorn.run directly with no
        # log_config, and the repository configures no basicConfig or
        # dictConfig, so root stays at WARNING with no handlers and an
        # application-side logger.info is dropped entirely (measured in a Pod:
        # zero [Toolbox] hits across 3000 log lines). This is one line per call
        # that carries response_format, so the volume stays manageable.
        logger.warning(
            "[Structured] path=native system_prompt=%s", bool(system_prompt)
        )
        return obj, "native"
    except SchemaError:
        # The schema itself is invalid. The request boundary already rejects
        # that with check_schema (models.py ResponseFormat) and this is the
        # backstop. The fallback path would fail just as surely, so raise
        # immediately instead of wasting a model call.
        raise
    except Exception as e:  # The model does not support structured output, or the native result was invalid
        logger.warning(
            "[Structured] native structured output failed or was invalid; "
            "falling back to prompt mode: %s", e
        )

    # 2. Prompt-forced JSON, validated, with a single retry
    # Stating that no tools are available for this call is necessary: the
    # system_prompt may contain skill sections, and the body load_skills injects
    # always says to call read_skill_file when needed. Extraction uses a bare
    # model with no tools bound, so a model that follows that instruction answers
    # in natural language instead of JSON and burns the single retry; if it does
    # so twice the whole task fails.
    # An instruction about how to answer, so it follows the system-prompt
    # boundary rather than the tool-description one; see the note in
    # core/skills.py and #826 ("Agent / LLM output language").
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
    raise RuntimeError(
        "structured output failed: not supported natively and the prompt "
        f"fallback was still invalid ({last_err})"
    )
