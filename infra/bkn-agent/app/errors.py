# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Client-facing error envelopes.

Every raise site names a catalog message key instead of literal text: the
public ``code`` stays stable across locales while description, detail, and
solution follow the request's effective locale. Several keys deliberately share
one code (ToolRef, Conflict, Mode) — the code is the machine contract, the key
only selects wording.
"""

from typing import Any, Optional

from fastapi import HTTPException

from app.commons.i18n import build_error_content, resource_name
from app.commons.locale import get_effective_locale


def err(status: int, message_key: str, /, *, code: Optional[str] = None, **params: Any) -> HTTPException:
    # status/message_key are positional-only and code is keyword-only so a
    # catalog placeholder may itself be named "status" or "code".
    return HTTPException(
        status_code=status,
        detail=build_error_content(message_key, code=code, **params),
    )


def not_found(resource: str, ident: str, /) -> HTTPException:
    """Raise the shared 404 envelope for a localized resource label."""
    return err(
        404,
        "BknAgent.NotFound",
        resource=resource_name(resource, get_effective_locale()),
        ident=ident,
    )


def bad_request(message_key: str, /, **params: Any) -> HTTPException:
    return err(400, message_key, **params)


def forbidden(message_key: str, /, **params: Any) -> HTTPException:
    return err(403, message_key, **params)
