from contextvars import ContextVar
from dataclasses import dataclass

from fastapi import Request

from app.errors import err

_ALLOWED_TYPES = {"user", "app"}

# The caller's Authorization header, forwarded verbatim.
#
# It lives here instead of being threaded through every signature: there are a
# dozen (account_id, account_type) positional parameters between the routers,
# the runner, and tool loading, and one more would rewrite the whole chain.
# A ContextVar is also copied into the context of an asyncio.create_task, so
# the background task behind /run still sees it.
#
# Who reads it: the Context Loader MCP surface accepts only a real token (the
# public surface runs introspect) and does not honour the /in header identity,
# so a type=context_loader tool calls with this forwarded token and keeps
# per-user authorization intact. See app/core/context_loader.py.
_caller_token: ContextVar[str | None] = ContextVar("bkn_agent_caller_token", default=None)


def caller_token() -> str | None:
    """The raw Authorization header of the current request, Bearer prefix
    included, or None when the caller sent none."""
    return _caller_token.get()


def set_caller_token(value: str | None):
    """Called from the HTTP middleware; see app/main.py.

    Deliberately not done inside get_account: that is a synchronous dependency,
    which FastAPI runs in a thread pool, and a ContextVar set in that pool never
    travels back to the request coroutine, so the value would always read None.
    """
    return _caller_token.set(value or None)


def reset_caller_token(token) -> None:
    _caller_token.reset(token)


@dataclass(frozen=True)
class Account:
    account_id: str
    account_type: str


def get_account(request: Request) -> Account:
    """The /in convention: the gateway trusts the headers and authorization is
    enforced downstream. An empty account fails closed, since this service is
    internal only.

    Authorization is not captured here. See the note on set_caller_token: a
    synchronous dependency runs in a thread pool and the ContextVar cannot
    travel back, so the middleware in app/main.py captures it instead.
    """
    account_id = (request.headers.get("x-account-id") or "").strip()
    account_type = (request.headers.get("x-account-type") or "").strip()
    if not account_id or account_type not in _ALLOWED_TYPES:
        raise err(401, "BknAgent.Auth.AccountRequired")
    return Account(account_id=account_id, account_type=account_type)
