from contextvars import ContextVar
from dataclasses import dataclass

from fastapi import Request

from app.errors import err

_ALLOWED_TYPES = {"user", "app"}

# 调用方原样带来的 Authorization 头。
#
# 存在这里而不是穿进每层函数签名：从路由到 runner 到工具装载有十几处
# (account_id, account_type) 位置参数，再加一个会把整条链全改一遍；而
# ContextVar 在 asyncio.create_task 时会随上下文复制，/run 的后台任务同样拿得到。
#
# 谁用它：Context Loader 的 MCP 面只认真实令牌（公开面挂 introspect），
# 不吃 /in 那套头部身份，所以 type=context_loader 工具靠这个透传的令牌调用，
# 保住 per-user 授权。见 app/core/context_loader.py。
_caller_token: ContextVar[str | None] = ContextVar("bkn_agent_caller_token", default=None)


def caller_token() -> str | None:
    """当前请求透传进来的 Authorization 头原文（含 Bearer 前缀），没有则 None。"""
    return _caller_token.get()


def set_caller_token(value: str | None):
    return _caller_token.set(value or None)


@dataclass(frozen=True)
class Account:
    account_id: str
    account_type: str


def get_account(request: Request) -> Account:
    """/in 约定：网关信任请求头，鉴权押下游。空账户 fail-closed（本服务仅内部）。

    另外把 Authorization 原样收进 ContextVar：本服务自身不校验它（身份仍以
    x-account-id 为准），只在需要令牌的下游（Context Loader MCP 公开面）原样转发。
    """
    set_caller_token(request.headers.get("authorization"))
    account_id = (request.headers.get("x-account-id") or "").strip()
    account_type = (request.headers.get("x-account-type") or "").strip()
    if not account_id or account_type not in _ALLOWED_TYPES:
        raise err(
            401,
            "Auth.AccountRequired",
            "缺少调用方身份",
            "x-account-id / x-account-type 请求头缺失或非法（anonymous 不被接受）",
            "bkn-agent 仅面向平台内部：平台模块以服务身份调用，内部工程师经网关携带 token / bak_ AppKey。",
        )
    return Account(account_id=account_id, account_type=account_type)
