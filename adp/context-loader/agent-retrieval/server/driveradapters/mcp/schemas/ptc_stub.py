# Copyright openbkn.ai
#
# Licensed under the Apache License, Version 2.0.
# See the LICENSE file in the project root for details.

"""BKN 能力的沙箱侧 stub —— 由 context-loader 生成，请勿手工编辑。

凭据与会话上下文经 _configure(event) 注入，由调用方在发起执行时下发。
"""

import json
import os
import pathlib
import urllib.request

_CFG = {}
_SESSION = {}

# 本次会话的工作目录。沙箱 /workspace 是所有调用方共用的一个目录（执行接口不接受
# session_id，池子实测恒命中同一个会话），直接在根上写 rows.json 这类名字，既会被
# 别的会话覆盖，也可能读到别人的数据。这里按 conversation 切出独立子目录并 chdir
# 进去，脚本用相对路径即可，无需关心隔离。
WORKDIR = pathlib.Path("/workspace")

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去。
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# 网关对此直接 400。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))


class ToolError(RuntimeError):
    """工具调用失败。message 为服务端原文，供模型据此修正参数后重试。"""


def _configure(event):
    """由执行入口调用，注入 MCP 端点、凭据与生命周期上下文，并准备工作目录。"""
    global WORKDIR
    _CFG.update(event)
    _SESSION.clear()

    # 目录名由 conversation_id 归一化而来，不做哈希：run_shell 走 language=shell，
    # 不经过本 stub，得在浏览器侧算出同一个路径才能进到同一个目录。而浏览器的
    # crypto.subtle 在非 HTTPS 源下不可用（部署常是裸 HTTP），算不了 sha1。
    # 归一化两边都能实现，且 ls /workspace 时还能直接看出是哪个对话。
    # 字符集写死成 ASCII 白名单，不用 c.isalnum()：Python 的 isalnum() 认 Unicode，
    # "名".isalnum() 为真，于是中文 conversation_id 在这里被原样保留，而 Go 侧
    # （run_shell 走那条路）只认 ASCII，会换成 -。两边就落到不同目录了。
    _SAFE = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
    conversation = str((event.get("bkn") or {}).get("conversation_id") or "").strip()
    safe = "".join(c if c in _SAFE else "-" for c in conversation)[:64]
    candidate = pathlib.Path("/workspace") / ("conv-" + safe if safe else "shared")
    try:
        candidate.mkdir(parents=True, exist_ok=True)
        os.chdir(candidate)
        WORKDIR = candidate
    except OSError:
        # 沙箱换了镜像、/workspace 只读之类的情况下不要连累整段脚本，退回当前目录。
        WORKDIR = pathlib.Path(os.getcwd())


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
    "Accept-Language": _CFG.get("locale", "zh-CN"),
    "Authorization": "Bearer " + _CFG["token"],
    }
    if _SESSION.get("id"):
        headers["Mcp-Session-Id"] = _SESSION["id"]
    request = urllib.request.Request(
        _CFG["mcp"], data=json.dumps(body).encode(),
        headers=headers, method="POST",
    )
    response = _OPENER.open(request, timeout=_CFG.get("timeout", 120))
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
    # 该上下文由调用方透传，模型无需感知，故不出现在函数签名里。
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
