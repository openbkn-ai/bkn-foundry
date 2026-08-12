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

每个函数对应一个 MCP 工具。只用标准库：MCP streamable HTTP 就是 JSON-RPC over
POST，urllib 足够，沙箱镜像无需预装任何依赖，也就没有 SDK 版本漂移的问题。

凭据与会话上下文经 _configure(event) 注入，由 agent 侧在发起执行时下发。
"""

import json
import urllib.request

_CFG = {}
_SESSION = {}

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去。
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# nginx 对此直接 400 —— 在装了系统代理的开发机上跑本地校验时必踩。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _configure(event):
    """由执行入口调用，注入 MCP 端点、凭据与生命周期上下文。"""
    _CFG.update(event)
    _SESSION.clear()


def _rpc(method, params=None, notify=False):
    body = {"jsonrpc": "2.0", "method": method}
    if params is not None:
        body["params"] = params
    if not notify:
        body["id"] = _SESSION.get("seq", 0) + 1
        _SESSION["seq"] = body["id"]
    headers = {
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
        "Authorization": "Bearer " + _CFG["token"],
    }
    if _SESSION.get("id"):
        headers["Mcp-Session-Id"] = _SESSION["id"]
    # 端点必须带尾斜杠：缺斜杠时服务端 307 跳转，而 urllib 不对 POST 跟随重定向。
    request = urllib.request.Request(
        _CFG["mcp"], data=json.dumps(body).encode(),
        headers=headers, method="POST",
    )
    timeout = _CFG.get("timeout", 120)
    response = _OPENER.open(request, timeout=timeout)
    if not _SESSION.get("id"):
        _SESSION["id"] = response.headers.get("Mcp-Session-Id")
    raw = response.read().decode()
    if not raw.strip():
        return None
    for line in raw.splitlines():
        if line.startswith("data: "):
            return json.loads(line[6:])
    return json.loads(raw)


def _ensure_session():
    """MCP 会话在模块级复用，一次执行内 initialize 只发生一次。"""
    if _SESSION.get("ready"):
        return
    _rpc("initialize", {
        "protocolVersion": "2025-06-18",
        "capabilities": {},
        "clientInfo": {"name": "bkn-sandbox", "version": "1"},
    })
    _rpc("notifications/initialized", {}, notify=True)
    _SESSION["ready"] = True


def _call(tool, args):
    """调用 MCP 工具。None 值不下发，交由服务端使用 schema 默认值。"""
    _ensure_session()
    payload = {k: v for k, v in args.items() if v is not None}
    # 业务类工具受会话守卫约束，缺 bkn_context 会被拒（conversation_required）。
    # 该上下文由 agent 透传，模型无需感知，故不出现在函数签名里。
    if _CFG.get("bkn"):
        payload["bkn_context"] = _CFG["bkn"]

    result = _rpc("tools/call", {"name": tool, "arguments": payload})["result"]
    text = "".join(c["text"] for c in result["content"] if c["type"] == "text")
    if result.get("isError") or result.get("is_error"):
        raise ToolError(tool + ": " + text)
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
        tools.append({"name": name, "meta": info, "schema": schema,
                      "output": raw.get("output_schema", {})})
    tools.sort(
        key=lambda t: (
            GROUP_ORDER.index(t["meta"]["group"])
            if t["meta"].get("group") in GROUP_ORDER
            else len(GROUP_ORDER),
            t["meta"].get("order", 0),
        )
    )
    return tools


def return_keys(tool: dict) -> str:
    """返回值顶层键。

    键名在各工具间并不统一（列表类有的叫 entries、有的叫 datas），模型无从推断，
    不写出来就只能猜——首次调用因 KeyError 失败正是这么来的。
    """
    props = tool.get("output", {}).get("properties") or {}
    return "{" + ", ".join(props) + "}" if props else "dict"


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
        "下列 BKN 能力已在作用域内，直接调用即可，无需 import。",
        "只有 stdout 会返回给你——中间结果不进上下文，因此请在脚本内完成过滤与聚合，",
        "只 print 你真正需要的内容。调用失败抛 `ToolError`。",
        "",
        "签名末尾的 `-> {…}` 是返回值顶层键。**其中部分键可能不出现**"
        "（如 `total_count` 在带过滤的查询里就没有），一律用 `.get()` 取，不要下标。",
        "过滤字段必须是该对象类真实的数据属性名——先用 `get_object_types` 查"
        "`data_properties`，不要按语义猜。",
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
        sig = signature(tool)[: -len(" -> dict")]
        lines.append(f"{sig} -> {return_keys(tool)}")
        lines.append(f"    # {meta.get('title', '')}")
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
        default=pathlib.Path("infra/bkn-agent/app/core"),
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
