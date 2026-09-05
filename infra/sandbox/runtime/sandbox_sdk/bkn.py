"""
sandbox_sdk.bkn —— 沙箱内的 BKN 能力面

让沙箱里的函数直接调用 BKN，而不必自己实现一遍 MCP 客户端：

【状态】不再推荐用于新函数——新函数用镜像里预装的 bkn-osdk（`from bkn_osdk import kn`），
它是本面的超集，且同一份代码在沙箱外也能跑。本面**仍受支持**，存量函数不必改写。

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
渲染出来的——与服务端 /mcp/info 报的 toolkit_version 走同一个 BuildPTCToolkit，逐字节
相同。渲染规则只此一份；用 Python 再实现一遍就又有了会各自漂移的第二份。

代价是产物与服务端工具面可能不同步。靠版本管理兜：产物里带
__toolkit_version__（内容哈希），CI 重新生成并比对，改了 schema 不重新生成
就红。运行时若怀疑镜像滞后，把 toolkit_version() 与服务端 /mcp/info 的
    toolkit_version 比一下即可。

【凭据与会话上下文从哪来】
默认走进程级环境变量（BKN_TOKEN / BKN_CONVERSATION_ID / BKN_INTERACTION_ID），
由执行工厂按请求里的 bkn_token 等字段注入。这样用户代码里看不到它们——event 是
业务入参，凭据混在里面既污染参数命名空间，也逼着每个想调 BKN 的人知道该往 event
里塞什么。

安全性与放 event 等价：executor 为每次执行现组一份环境再 --setenv 进 bwrap，
随进程消亡。**注意区分建会话时的 env_vars**，那个是容器级的、会跨调用方存活，
所以这里 event 优先于 env：万一某个池化容器上留着旧令牌，调用方显式传的那份得赢。

MCP 地址是部署配置而非密钥，允许回退到 BKN_SANDBOX_MCP_URL，由控制面注入一次即可。
定义虽已烤进镜像，调用仍要回访 MCP，地址少不了。
"""

from __future__ import annotations

import json
import os
import urllib.request
from typing import Any, Optional

# MCP 地址的部署级兜底。集群内地址，沙箱用不了浏览器侧的网关地址。
_MCP_URL_ENV = "BKN_SANDBOX_MCP_URL"

# 调用方身份与会话上下文，由执行工厂按请求里的 bkn_token / bkn_conversation_id /
# bkn_interaction_id 转成本次执行的进程级环境变量。
#
# 走 env 而不是 event，是为了让用户代码里看不到它们：event 是业务入参，凭据混在
# 里面既污染参数命名空间，也逼着每个想调 BKN 的人知道该往 event 里塞什么。
#
# 安全性与放在 event 里等价：executor 为每次执行现组一份环境再 --setenv 进 bwrap，
# 随进程消亡。要与建会话时的 env_vars 区分——那个是容器级的，会跨调用方存活，
# 所以下面 event 优先于 env：万一某个池化容器上留着旧令牌，调用方显式传的那份得赢。
_TOKEN_ENV = "BKN_TOKEN"
_CONVERSATION_ENV = "BKN_CONVERSATION_ID"
_INTERACTION_ENV = "BKN_INTERACTION_ID"
_PARENT_OPERATION_ENV = "BKN_PARENT_OPERATION_ID"

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
    # event 优先：它是本次调用显式带来的，而环境变量理论上可能来自建会话时的
    # 容器级配置（那份会跨调用方存活）。
    token = str(_RUNTIME.get("token") or "").strip()
    if not token:
        token = os.environ.get(_TOKEN_ENV, "").strip()
    if not token:
        raise BKNNotConfigured(
            "没有调用方令牌：调用执行接口时传 bkn_token（推荐），或在 event 里传 token。"
        )
    return token


def _business_context() -> dict:
    """本次调用的会话上下文，随每次 BKN 调用带给服务端，操作因此挂得到同一次交互。"""
    context = dict(_RUNTIME.get("bkn") or {})
    for key, env_name in (
        ("conversation_id", _CONVERSATION_ENV),
        ("interaction_id", _INTERACTION_ENV),
    ):
        if not str(context.get(key) or "").strip():
            value = os.environ.get(env_name, "").strip()
            if value:
                context[key] = value
    # An environment parent belongs to this execution's turn. An explicit
    # runtime context for another turn must not inherit that parent.
    if (
        not str(context.get("parent_operation_id") or "").strip()
        and context.get("conversation_id")
        and context.get("interaction_id")
        and context.get("conversation_id") == os.environ.get(_CONVERSATION_ENV, "").strip()
        and context.get("interaction_id") == os.environ.get(_INTERACTION_ENV, "").strip()
    ):
        parent = os.environ.get(_PARENT_OPERATION_ENV, "").strip()
        if parent:
            context["parent_operation_id"] = parent
    return context


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
    configured["bkn"] = _business_context()
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
    """本次执行能否调用 BKN。用于在纯计算路径上做条件分支。"""  # noqa: D401
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
