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


async def structured_extract(model, messages: list, schema: dict) -> dict:
    """从 messages 抽出符合 schema 的对象。model 应为非流式（见 build_chat_model）。"""
    # 1. 原生
    try:
        norm = normalize_response_format(schema)
        r = await model.with_structured_output(norm).ainvoke(messages)
        obj = r.model_dump() if hasattr(r, "model_dump") else dict(r)
        # 原生也校验：with_structured_output 未启 strict，可能缺 required/类型不符；
        # 不合法则不当成功返回，落到下面提示词降级重试。
        _jsonschema_validate(obj, schema)
        return obj
    except SchemaError:
        # schema 本体非法：请求边界已用 check_schema 拦（models.py ResponseFormat），
        # 这里兜底。降级路径同样必炸，直接抛出，不白费模型调用。
        raise
    except Exception as e:  # 模型不支持结构化或原生结果不合法 → 降级
        logger.warning("[Structured] 原生结构化失败/不合法，降级到提示词模式：%s", e)

    # 2. 提示词强制 JSON + 校验 + 重试一次
    instr = (
        "请只输出一个 JSON 对象，严格符合下面的 JSON Schema。"
        "不要 markdown 代码块，不要任何多余文字或解释：\n"
        + json.dumps(schema, ensure_ascii=False)
    )
    msgs = list(messages) + [("user", instr)]
    last_err: Any = None
    for _ in range(2):
        resp = await model.ainvoke(msgs)
        text = resp.content if isinstance(resp.content, str) else str(resp.content)
        try:
            obj = _extract_json(text)
            _jsonschema_validate(obj, schema)
            return obj
        except (json.JSONDecodeError, ValidationError, ValueError) as e:
            last_err = e
            msgs = msgs + [
                ("assistant", text),
                ("user", f"上面的输出不合法（{e}）。请重新只输出严格符合 schema 的 JSON。"),
            ]
    raise RuntimeError(f"结构化输出失败：原生不支持且提示词降级仍不合法（{last_err}）")
