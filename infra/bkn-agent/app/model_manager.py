# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Resolve models visible to the current caller through mf-model-manager."""

from dataclasses import dataclass

import aiohttp

from app.auth import caller_token
from app.config import config
from app.errors import err


@dataclass(frozen=True)
class ResolvedModel:
    """The stable model identity returned by the caller-authorized catalog."""

    model_id: str
    model_type: str
    model_name: str


async def resolve_authorized_model(model_id: str) -> ResolvedModel:
    """Resolve a caller-visible model without trusting browser-supplied metadata."""
    token = caller_token()
    if not token:
        raise err(401, "BknAgent.Auth.TokenRequired")
    try:
        async with aiohttp.ClientSession(
            timeout=aiohttp.ClientTimeout(total=5)
        ) as client:
            async with client.get(
                f"{config.MF_MODEL_MANAGER_PUBLIC_BASE}/llm/get",
                params={"model_id": model_id},
                headers={"Authorization": token},
                allow_redirects=False,
            ) as response:
                if response.status in {400, 404}:
                    raise err(400, "BknAgent.Execution.Unsupported")
                if response.status == 401:
                    raise err(401, "BknAgent.Auth.TokenRequired")
                if response.status == 403:
                    raise err(403, "BknAgent.Execution.Forbidden")
                if response.status != 200:
                    raise err(503, "BknAgent.Execution.Unavailable")
                data = await response.json()
    except (aiohttp.ClientError, TimeoutError, ValueError):
        raise err(503, "BknAgent.Execution.Unavailable") from None
    if not isinstance(data, dict) or str(data.get("model_id")) != model_id:
        raise err(400, "BknAgent.Execution.Unsupported")
    model_type = data.get("model_type")
    model_name = data.get("model_name")
    if (
        not isinstance(model_type, str)
        or not 0 < len(model_type) <= 64
        or model_type != model_type.strip()
        or not isinstance(model_name, str)
        or not 0 < len(model_name) <= 200
        or model_name != model_name.strip()
    ):
        raise err(400, "BknAgent.Execution.Unsupported")
    return ResolvedModel(
        model_id=model_id,
        model_type=model_type,
        model_name=model_name,
    )
