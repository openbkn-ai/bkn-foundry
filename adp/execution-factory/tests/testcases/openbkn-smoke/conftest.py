# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""OpenBKN smoke fixtures — no KWeaver platform (eisoo/Hydra) required."""

import os

import pytest


def _build_headers() -> dict[str, str]:
    business_domain = os.environ.get("OPENBKN_BUSINESS_DOMAIN", "bd_public")
    auth_disabled = os.environ.get("OPENBKN_AUTH_ENABLED", "").lower() == "false"

    if auth_disabled:
        return {
            "x-account-id": os.environ.get("OPENBKN_ACCOUNT_ID", "openbkn-smoke"),
            "x-account-type": "user",
            "x-business-domain": business_domain,
        }

    token = os.environ.get("OPENBKN_TOKEN", "").strip()
    if not token:
        pytest.skip(
            "Set OPENBKN_TOKEN, or OPENBKN_AUTH_ENABLED=false for local dev without Hydra."
        )

    if not token.lower().startswith("bearer "):
        token = f"Bearer {token}"

    return {
        "Authorization": token,
        "x-business-domain": business_domain,
    }


@pytest.fixture(scope="session")
def Headers():
    return _build_headers()
