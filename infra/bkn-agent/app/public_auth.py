# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Credential and resource checks for the public execution routes only."""

import aiohttp
from fastapi import Depends, Request
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer

from app import observability
from app.auth import Account
from app.commons import locale
from app.config import config
from app.errors import err


async def _post(url: str, **kwargs) -> dict:
    try:
        async with aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=10)
        ) as client:
            async with client.post(
                url,
                headers=locale.internal_request_headers(
                    observability.outbound_headers()
                ),
                allow_redirects=False,
                **kwargs,
            ) as response:
                if response.status != 200:
                    raise err(503, "BknAgent.Execution.Unavailable")
                result = await response.json()
                if not isinstance(result, dict):
                    raise ValueError("invalid identity/authorization response")
                return result
    except (aiohttp.ClientError, TimeoutError, ValueError):
        raise err(503, "BknAgent.Execution.Unavailable") from None


_bearer = HTTPBearer(auto_error=False)


async def get_public_account(
    request: Request,
    _credentials: HTTPAuthorizationCredentials | None = Depends(_bearer),
) -> Account:
    authorization = request.headers.get("authorization", "")
    parts = authorization.split()
    if len(parts) != 2 or parts[0].lower() != "bearer":
        raise err(401, "BknAgent.Auth.TokenRequired")
    token = parts[1]
    if token.startswith("bak_"):
        info = await _post(
            f"{config.BKN_SAFE_BASE}/api-keys/introspect", json={"token": token}
        )
        kind = info.get("account_type")
        if not isinstance(kind, str) or not kind:
            raise err(401, "BknAgent.Auth.TokenRequired")
        # Match Context Loader: bkn-safe calls an ordinary user "other";
        # all non-app owners resolve to the same person-like execution subject.
        account_type = "app" if kind == "app" else "user"
    else:
        info = await _post(
            f"{config.HYDRA_ADMIN_BASE}/oauth2/introspect", data={"token": token}
        )
        extension = info.get("ext") or {}
        if not isinstance(extension, dict):
            raise err(503, "BknAgent.Execution.Unavailable")
        visitor = extension.get("visitor_type")
        if visitor is not None and not isinstance(visitor, str):
            raise err(401, "BknAgent.Auth.TokenRequired")
        account_type = (
            "app"
            if info.get("client_id") and info.get("sub") == info["client_id"]
            else {"realname": "user", "business": "app"}.get(visitor)
        )
    subject = info.get("sub")
    if (
        info.get("active") is not True
        or not isinstance(subject, str)
        or not subject.strip()
        or subject != subject.strip()
        or not account_type
    ):
        raise err(401, "BknAgent.Auth.TokenRequired")
    # Downstream handlers see the same verified identity, never header aliases
    # supplied by a browser. The Authorization header itself is forwarded intact.
    headers = [
        (key, value)
        for key, value in request.scope["headers"]
        if key.lower() not in {b"x-account-id", b"x-account-type", b"user_id"}
    ]
    headers.extend(
        [
            (b"x-account-id", subject.encode()),
            (b"x-account-type", account_type.encode()),
            (b"user_id", subject.encode()),
        ]
    )
    request.scope["headers"] = headers
    return Account(subject, account_type)


async def authorize_execution(account: Account, agent_id: str, kn_id: str) -> None:
    checks = [
        {"resource": {"type": "agent", "id": agent_id}, "operation": "use"},
        {
            "resource": {"type": "knowledge_network", "id": kn_id},
            "operation": "view_detail",
        },
    ]
    result = await _post(
        f"{config.BKN_SAFE_BASE}/authz/checks",
        json={"accessor_id": account.account_id, "checks": checks},
    )
    decisions = result.get("results")
    if not isinstance(decisions, list) or len(decisions) != len(checks):
        raise err(503, "BknAgent.Execution.Unavailable")
    for check in checks:
        matching = [
            d
            for d in decisions
            if isinstance(d, dict)
            and d.get("resource_type") == check["resource"]["type"]
            and d.get("resource_id") == check["resource"]["id"]
            and d.get("operation") == check["operation"]
        ]
        if len(matching) != 1:
            raise err(503, "BknAgent.Execution.Unavailable")
        if matching[0].get("allowed") is not True:
            raise err(403, "BknAgent.Execution.Forbidden")
    if result.get("allowed") is not True:
        raise err(403, "BknAgent.Execution.Forbidden")
