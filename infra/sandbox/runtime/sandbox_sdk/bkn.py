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

【为什么函数定义不烤进镜像】
本模块只有加载器，那 21 个 BKN 函数的定义由 context-loader 在
`GET /mcp/ptc/toolkit` 渲染，首次使用时取回并按 version（内容哈希）缓存到
/workspace。烤进镜像的话，工具面一变镜像就漂，且只能靠重建镜像来修；而工具面
是随服务演进的，漂移只会在模型调用失败时才暴露。

【为什么凭据只认 event，不读环境变量】
沙箱会话是池化复用的，环境变量会把上一个调用方的值留在容器里。task_id 这类
追踪标记漏了无伤大雅（Context.from_env 正是这么做的），但令牌漏了是凭据泄露。
所以本模块只从 event 取 token，取不到就直接报错，不做任何回退。

MCP 地址是另一回事：它是部署配置而非密钥，因此允许 event 缺省时回退到沙箱级
环境变量，由控制面注入一次即可，不必每个调用方都传。
"""

from __future__ import annotations

import json
import os
import pathlib
import urllib.request
from typing import Any, Optional

# 工具包缓存目录。/workspace 是持久的（对象存储挂载），所以拉一次能长期复用。
_CACHE_DIR = pathlib.Path("/workspace/.bkn-toolkit")

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


def _load_stub_source() -> str:  # noqa: C901
    """取回能力面的 Python 源码，按 version 缓存。

    先读缓存里的 version 再决定要不要写盘：工具包每次都取（那是一个小 GET），
    但 27K 的源码只在内容真的变了时才落盘，免得每次执行都写一遍对象存储。
    """
    global _VERSION
    toolkit = _fetch_toolkit()
    version = str(toolkit.get("version") or "").strip()
    _VERSION = version or None
    stub = toolkit.get("stub")
    if not stub:
        raise BKNNotConfigured("工具包里没有 stub 字段，无法装配能力面。")

    if version:
        # version 是内容哈希，直接做文件名，不同版本自然并存、互不覆盖。
        safe = "".join(c if c.isalnum() or c in "-_" else "-" for c in version)[:80]
        path = _CACHE_DIR / (safe + ".py")
        try:
            if path.exists():
                return path.read_text(encoding="utf-8")
            _CACHE_DIR.mkdir(parents=True, exist_ok=True)
            # 先写临时文件再改名：并发执行下别让另一个进程读到写了一半的源码。
            tmp = path.with_suffix(".py.%d.tmp" % os.getpid())
            tmp.write_text(stub, encoding="utf-8")
            tmp.replace(path)
        except OSError:
            # /workspace 只读或满了都不该连累这次调用，直接用内存里的源码。
            pass
    return stub


def _impl():
    """惰性装配能力面。不碰 BKN 的纯计算函数因此零负担。"""
    global _IMPL
    if _IMPL is not None:
        return _IMPL

    import importlib.util

    source = _load_stub_source()
    spec = importlib.util.spec_from_loader("sandbox_sdk._bkn_impl", loader=None)
    module = importlib.util.module_from_spec(spec)
    exec(compile(source, "<bkn-toolkit>", "exec"), module.__dict__)

    # stub 用 _configure(event) 接收 MCP 地址、凭据与 bkn_context；这里把地址补齐
    # 成带尾斜杠的形态再交过去，免得调用方传了个缺斜杠的地址在深处才炸。
    configured = dict(_RUNTIME)
    configured["mcp"] = _mcp_url()
    configured["token"] = _token()
    configured.setdefault("bkn", {})
    module._configure(configured)

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
    """当前装配的能力面版本，未装配时为 None。

    版本由加载器记录而不是从 stub 里读：stub 是渲染产物，不含自身版本，
    指望它自报家门只会恒得到 None。
    """
    return _VERSION
