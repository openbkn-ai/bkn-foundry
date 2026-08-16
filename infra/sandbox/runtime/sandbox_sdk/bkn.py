"""
sandbox_sdk.bkn —— 沙箱内的 BKN 能力面

让沙箱里的函数直接调用 BKN，而不必自己实现一遍 MCP 客户端：

    from sandbox_sdk import tool, bkn

    @tool
    def top_teams(kn_id: str, limit: int = 3) -> dict:
        "取该知识网络里进球最多的若干支球队。"
        res = bkn.list_resources(kn_id=kn_id)
        ...

用户看不到 event、token、conversation_id —— dispatch() 在调用用户函数前已经把
本次执行的 event 交给了本模块（见 __init__.configure_runtime）。

【能力面从哪来】
_bkn_tools.py 与本模块一同随镜像发布，沙箱因此不依赖网络就能 import。
那份 Python 不是手写的，是构建期由 `go run ./cmd/ptc-stub` 从 MCP 工具目录
渲染出来的——与线上 `GET /mcp/ptc/toolkit` 走同一个 BuildPTCToolkit，逐字节
相同。渲染规则只此一份；用 Python 再实现一遍就又有了会各自漂移的第二份。

代价是产物与服务端工具面可能不同步。靠版本管理兜：产物里带
__toolkit_version__（内容哈希），CI 重新生成并比对，改了 schema 不重新生成
就红。运行时也能自查，见 check_against_server()。

【为什么凭据只认 event，不读环境变量】
沙箱会话是池化复用的，环境变量会把上一个调用方的值留在容器里。task_id 这类
追踪标记漏了无伤大雅（Context.from_env 正是这么做的），但令牌漏了是凭据泄露。
所以本模块只从 event 取 token，取不到就直接报错，不做任何回退。

MCP 地址是另一回事：它是部署配置而非密钥，因此允许 event 缺省时回退到沙箱级
环境变量，由控制面注入一次即可。注意定义虽已烤进镜像，调用仍要回访 MCP，
地址少不了。
"""

from __future__ import annotations

import json
import os
import urllib.request
from typing import Any, Optional

# MCP 地址的部署级兜底。集群内地址，沙箱用不了浏览器侧的网关地址。
_MCP_URL_ENV = "BKN_SANDBOX_MCP_URL"

# 取工具包用的地址由 MCP 地址推导：/mcp/ 与 /mcp/ptc/toolkit 同源。
_TOOLKIT_SUFFIX = "ptc/toolkit"

# 显式不走代理：MCP 端点是集群内地址，任何继承来的代理配置都只会让请求发不出去；
# 且 urllib 一旦认定要走代理就改发 absolute-form 请求行（POST http://host/path），
# 网关对此直接 400。
_OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))

_RUNTIME: dict = {}
_IMPL = None
_VERSION: Optional[str] = None


class BKNNotConfigured(RuntimeError):
    """本次执行没拿到调用 BKN 所需的信息。"""


def configure_runtime(event: dict) -> None:
    """由 dispatch() 在调用用户函数之前调用，交出本次执行的 event。

    每次执行都重置：沙箱会话池化复用，上一个调用方的凭据不能留在进程里。
    """
    global _RUNTIME, _IMPL, _VERSION
    _RUNTIME = event or {}
    _IMPL = None
    _VERSION = None


def _mcp_url() -> str:
    url = str(_RUNTIME.get("mcp") or os.environ.get(_MCP_URL_ENV) or "").strip()
    if not url:
        raise BKNNotConfigured(
            "没有 MCP 地址：请在 event 里传 mcp，或由控制面设置环境变量 "
            f"{_MCP_URL_ENV}。"
        )
    # 尾斜杠必须有：缺了网关会 307 跳转，而 urllib 不对 POST 跟随重定向，
    # 症状是一个没有报文的 400。
    return url if url.endswith("/") else url + "/"


def _token() -> str:
    token = str(_RUNTIME.get("token") or "").strip()
    if not token:
        raise BKNNotConfigured(
            "没有调用方令牌：请在 event 里传 token。"
            "本模块不从环境变量取令牌——沙箱会话池化复用，env 会把上一个调用方的值"
            "留在容器里。"
        )
    return token


def _fetch_toolkit() -> dict:
    request = urllib.request.Request(
        _mcp_url() + _TOOLKIT_SUFFIX,
        headers={"Authorization": "Bearer " + _token()},
    )
    with _OPENER.open(request, timeout=30) as response:
        return json.loads(response.read().decode("utf-8"))


def _load_capability_module():
    """载入随镜像发布的能力面产物。单独一个函数，测试才好把它换掉。"""
    from . import _bkn_tools

    return _bkn_tools


def _impl():
    """装配能力面。惰性——不碰 BKN 的纯计算函数因此零负担。"""
    global _IMPL, _VERSION
    if _IMPL is not None:
        return _IMPL

    try:
        module = _load_capability_module()
    except ImportError as exc:
        raise BKNNotConfigured(
            "能力面未随镜像发布（sandbox_sdk/_bkn_tools.py 缺失）。"
            "重新生成：make -C infra/sandbox bkn-tools"
        ) from exc

    # 每次执行都重新 _configure：模块是进程级单例，而沙箱会话池化复用，
    # 上一个调用方的凭据不能留在里面。地址在这里补齐尾斜杠，免得调用方传了个
    # 缺斜杠的地址到深处才炸。
    configured = dict(_RUNTIME)
    configured["mcp"] = _mcp_url()
    configured["token"] = _token()
    configured.setdefault("bkn", {})
    module._configure(configured)

    _VERSION = getattr(module, "__toolkit_version__", None)
    _IMPL = module
    return _IMPL


def __getattr__(name: str) -> Any:
    """把 bkn.xxx 转给能力面。首次访问时才装配。"""
    if name.startswith("_"):
        raise AttributeError(name)
    return getattr(_impl(), name)


def __dir__() -> list:
    try:
        return sorted(n for n in dir(_impl()) if not n.startswith("_"))
    except Exception:
        # 没配置好时也要能 dir()，否则 REPL 与补全会抛出难懂的错。
        return []


def available() -> bool:
    """本次执行能否调用 BKN。用于在纯计算路径上做条件分支。"""
    try:
        _mcp_url()
        _token()
        return True
    except BKNNotConfigured:
        return False


def toolkit_version() -> Optional[str]:
    """本镜像里能力面的版本（内容哈希），未装配时为 None。

    值由 cmd/ptc-stub 在构建期写进产物，不是运行时算的。
    """
    return _VERSION


def check_against_server() -> dict:
    """比对镜像里的能力面与服务端当前工具面是否同版本。

    诊断用，正常调用路径不会碰它——那条路完全离线。镜像滞后于服务端时，
    症状是签名对不上导致调用失败，而失败信息里看不出根因，所以留一个能直接问的口子。
    """
    local = toolkit_version() or getattr(_impl(), "__toolkit_version__", None)
    remote = str(_fetch_toolkit().get("version") or "") or None
    return {"local": local, "remote": remote, "in_sync": bool(local and local == remote)}
