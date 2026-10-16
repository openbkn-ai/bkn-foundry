"""Regression coverage for independent REST and MCP deployment addresses."""

from types import SimpleNamespace
from unittest.mock import patch

import pytest

from src.infrastructure.config.settings import resolve_bkn_base_url


@pytest.mark.parametrize(
    ("base", "mcp", "expected"),
    [
        (
            " http://gateway.edge.svc:80/ ",
            "http://retrieval:30780/api/agent-retrieval/v1/mcp/",
            "http://gateway.edge.svc:80",
        ),
        ("", "http://retrieval:30780/api/agent-retrieval/v1/mcp/", ""),
        ("  ", "http://retrieval:30780", ""),
        ("", "", ""),
    ],
)
def test_rest_base_is_never_derived_from_mcp(base, mcp, expected):
    settings = SimpleNamespace(bkn_base_url=base, bkn_sandbox_mcp_url=mcp)
    with patch("src.infrastructure.config.settings.get_settings", return_value=settings):
        assert resolve_bkn_base_url() == expected
