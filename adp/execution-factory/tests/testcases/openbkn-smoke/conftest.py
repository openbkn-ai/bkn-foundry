# -*- coding: utf-8 -*-
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""OpenBKN smoke fixtures."""

import os

import pytest


def _build_headers() -> dict[str, str]:
    token = os.environ.get("OPENBKN_TOKEN", "").strip()
    if not token:
        pytest.skip("Set OPENBKN_TOKEN to run authenticated smoke tests.")

    if not token.lower().startswith("bearer "):
        token = f"Bearer {token}"

    return {
        "Authorization": token,
    }


@pytest.fixture(scope="session")
def Headers():
    return _build_headers()
