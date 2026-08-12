#!/usr/bin/env python3
"""从 MCP schema 生成沙箱侧工具 stub 与工具摘要。

输入：driveradapters/mcp/schemas/ 下的 tools_meta.json 与各工具的 <tool>.json
输出：
  _tools.py       —— 沙箱内可 import 的同步函数，内封 MCP 客户端
  tool_digest.md  —— run_code 工具 description 用的签名清单

产物是编译结果，不接受手工编辑；改 schema 后重跑本脚本。
用法：python3 gen_sandbox_tools.py [--out DIR] [--check]
"""

from __future__ import annotations

import argparse
import json
import pathlib
import sys

SCHEMA_DIR = (
    pathlib.Path(__file__).resolve().parent.parent
    / "server/driveradapters/mcp/schemas"
)

# 生命周期工具的 schema 定义在 Go 代码内（schemas.go），不在 schemas 目录；
# 且沙箱沿用 agent 侧已开启的 interaction，不自行开关，故不生成。
SKIP_TOOLS = {"bkn_start_interaction", "bkn_finish_interaction"}

PY_TYPES = {
    "string": "str",
    "array": "list",
    "object": "dict",
    "boolean": "bool",
    "integer": "int",
    "number": "float",
}

GROUP_ORDER = ["discovery", "query", "action", "resource"]

# schema 默认 response_format=toon —— 那是为「直接喂给模型」优化的省 token 文本格式。
# 代码模式下返回值先经脚本处理，需要的是可下标访问的结构，故覆盖为 json。
DEFAULT_OVERRIDES = {"response_format": "json"}

RUNTIME_PREAMBLE = '''"""BKN 能力的沙箱侧 stub —— 由 gen_sandbox_tools.py 生成，请勿手工编辑。

每个函数对应一个 MCP 工具。MCP 会话在模块级复用：一次执行内的多次调用共用
同一个 session，initialize 握手只发生一次。
"""

from __future__ import annotations

import asyncio
import atexit
import contextlib
import json
import os

from mcp import ClientSession
from mcp.client.streamable_http import (
    create_mcp_http_client,
    streamable_http_client,
)

_MCP_URL = os.environ["BKN_MCP_URL"]
_TOKEN = os.environ["BKN_MCP_TOKEN"]

_loop: asyncio.AbstractEventLoop | None = None
_stack: contextlib.AsyncExitStack | None = None
_session: ClientSession | None = None


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _get_loop() -> asyncio.AbstractEventLoop:
    global _loop
    if _loop is None:
        _loop = asyncio.new_event_loop()
        atexit.register(_shutdown)
    return _loop


async def _get_session() -> ClientSession:
    global _stack, _session
    if _session is None:
        _stack = contextlib.AsyncExitStack()
        http_client = await _stack.enter_async_context(
            create_mcp_http_client(
                headers={"Authorization": f"Bearer {_TOKEN}"}
            )
        )
        read, write = await _stack.enter_async_context(
            streamable_http_client(_MCP_URL, http_client=http_client)
        )
        _session = await _stack.enter_async_context(ClientSession(read, write))
        await _session.initialize()
    return _session


def _shutdown() -> None:
    global _stack, _session
    if _stack is not None and _loop is not None:
        with contextlib.suppress(Exception):
            _loop.run_until_complete(_stack.aclose())
    _stack = _session = None


async def _call_async(tool: str, args: dict):
    session = await _get_session()
    return await session.call_tool(tool, args)


def _business_context() -> dict | None:
    """会话生命周期上下文。

    服务端要求业务类工具调用携带 bkn_context（conversation_id + interaction_id），
    否则返回 conversation_required / interaction_required。该上下文由 agent 侧在
    发起本次执行时透传进沙箱环境，模型无需感知，也不出现在函数签名里。
    """
    conversation = os.environ.get("BKN_CONVERSATION_ID", "").strip()
    interaction = os.environ.get("BKN_INTERACTION_ID", "").strip()
    if not conversation or not interaction:
        return None
    return {"conversation_id": conversation, "interaction_id": interaction}


def _call(tool: str, args: dict):
    """调用 MCP 工具。None 值不下发，交由服务端使用 schema 默认值。"""
    payload = {k: v for k, v in args.items() if v is not None}
    context = _business_context()
    if context is not None:
        payload["bkn_context"] = context
    result = _get_loop().run_until_complete(_call_async(tool, payload))

    text = "".join(
        block.text
        for block in result.content
        if getattr(block, "type", "") == "text"
    )
    if result.is_error:
        raise ToolError(f"{tool}: {text}")
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        # response_format=toon 等非 JSON 形态按原文返回
        return text
'''


def load_tools() -> list[dict]:
    meta_file = SCHEMA_DIR / "tools_meta.json"
    meta = json.loads(meta_file.read_text(encoding="utf-8"))
    tools = []
    for name, info in meta.items():
        if name in SKIP_TOOLS:
            continue
        schema_file = SCHEMA_DIR / f"{name}.json"
        if not schema_file.exists():
            print(f"warn: {name} 无 schema 文件，跳过", file=sys.stderr)
            continue
        raw = json.loads(schema_file.read_text(encoding="utf-8"))
        schema = raw["input_schema"]
        tools.append({"name": name, "meta": info, "schema": schema})
    tools.sort(
        key=lambda t: (
            GROUP_ORDER.index(t["meta"]["group"])
            if t["meta"].get("group") in GROUP_ORDER
            else len(GROUP_ORDER),
            t["meta"].get("order", 0),
        )
    )
    return tools


def signature(tool: dict) -> str:
    props = tool["schema"].get("properties", {})
    required = tool["schema"].get("required", [])
    args = [
        f"{k}: {PY_TYPES.get(v.get('type'), 'object')}"
        for k, v in props.items()
        if k in required
    ]
    args += [
        f"{k}: {PY_TYPES.get(v.get('type'), 'object')} = "
        f"{DEFAULT_OVERRIDES.get(k, v.get('default'))!r}"
        for k, v in props.items()
        if k not in required
    ]
    return f"{tool['name']}({', '.join(args)}) -> dict"


def render_function(tool: dict) -> str:
    props = tool["schema"].get("properties", {})
    doc_lines = [tool["meta"].get("description", "").strip(), ""]
    doc_lines += [
        f"{k}: {v.get('description', '').strip()}" for k, v in props.items()
    ]
    doc = "\n    ".join(line for line in doc_lines)
    body = ", ".join(f'"{k}": {k}' for k in props)
    return (
        f"def {signature(tool)}:\n"
        f'    """{doc}\n    """\n'
        f'    return _call("{tool["name"]}", {{{body}}})\n'
    )


def render_tools_py(tools: list[dict]) -> str:
    parts = [RUNTIME_PREAMBLE, "\n"]
    parts += [render_function(t) + "\n" for t in tools]
    parts.append(
        "__all__ = [\n"
        + "".join(f'    "{t["name"]}",\n' for t in tools)
        + '    "ToolError",\n]\n'
    )
    return "\n".join(parts)


def render_digest(tools: list[dict]) -> str:
    lines = [
        "<!-- 由 gen_sandbox_tools.py 生成，请勿手工编辑 -->",
        "",
        "工作目录已预置 `_tools.py`，直接 import 即可调用 BKN 能力。",
        "只有 stdout 会返回给你——中间结果不进上下文，因此请在脚本内完成过滤与聚合，",
        "只 print 你真正需要的内容。",
        "",
        "## 可用函数",
        "",
    ]
    current = None
    for tool in tools:
        meta = tool["meta"]
        group = meta.get("group_title") or meta.get("group", "")
        if group != current:
            if current is not None:
                lines += ["```", ""]
            lines += [f"### {group}", "", "```python"]
            current = group
        lines.append(f"{signature(tool)}")
        lines.append(f"    # {tool['meta'].get('title', '')}")
    lines += ["```", ""]
    lines += [
        "## 调用顺序",
        "",
        "`kn_id`、`ot_id` 不能凭空写，必须先查：",
        "",
        "```text",
        "list_knowledge_networks  → kn_id",
        "get_kn_detail(kn_id)     → object_types 概览",
        "get_object_types(...)    → 属性定义与可用算子",
        "```",
        "",
        "## 参数写不准时",
        "",
        "每个函数的完整 schema 在 docstring 里，脚本内自查，不要猜：",
        "",
        "```python",
        "help(query_object_instance)",
        "```",
        "",
        "特别是 `condition` 的 `operation`：`match` / `knn` 能否使用取决于该属性的",
        "`condition_operations`（见 `get_object_types` 返回），从 `type` 推不出来。",
        "",
        "## 错误处理",
        "",
        "调用失败抛 `ToolError`，message 为服务端原文。可在脚本内捕获并修正参数重试，",
        "不必回到对话轮次。",
        "",
    ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument(
        "--out",
        type=pathlib.Path,
        default=pathlib.Path("infra/sandbox/runtime/sandbox_sdk/bkn_tools"),
        help="产物输出目录",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="只校验产物是否与 schema 一致，不写文件；不一致时退出码非 0",
    )
    args = parser.parse_args()

    tools = load_tools()
    outputs = {
        "_tools.py": render_tools_py(tools),
        "tool_digest.md": render_digest(tools),
    }

    if args.check:
        stale = [
            name
            for name, content in outputs.items()
            if not (args.out / name).exists()
            or (args.out / name).read_text(encoding="utf-8") != content
        ]
        if stale:
            print(
                f"产物已过期，请重跑 gen_sandbox_tools.py：{', '.join(stale)}",
                file=sys.stderr,
            )
            return 1
        print(f"产物与 schema 一致（{len(tools)} 个工具）")
        return 0

    args.out.mkdir(parents=True, exist_ok=True)
    for name, content in outputs.items():
        (args.out / name).write_text(content, encoding="utf-8")
    print(f"已生成 {len(tools)} 个工具 -> {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
