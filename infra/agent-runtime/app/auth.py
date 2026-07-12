from dataclasses import dataclass

from fastapi import Request

from app.errors import err

_ALLOWED_TYPES = {"user", "app"}


@dataclass(frozen=True)
class Account:
    account_id: str
    account_type: str


def get_account(request: Request) -> Account:
    """/in 约定：网关信任请求头，鉴权押下游。空账户 fail-closed（本服务仅内部）。"""
    account_id = (request.headers.get("x-account-id") or "").strip()
    account_type = (request.headers.get("x-account-type") or "").strip()
    if not account_id or account_type not in _ALLOWED_TYPES:
        raise err(
            401,
            "Auth.AccountRequired",
            "缺少调用方身份",
            "x-account-id / x-account-type 请求头缺失或非法（anonymous 不被接受）",
            "agent-runtime 仅面向平台内部：平台模块以服务身份调用，内部工程师经网关携带 token / bak_ AppKey。",
        )
    return Account(account_id=account_id, account_type=account_type)
