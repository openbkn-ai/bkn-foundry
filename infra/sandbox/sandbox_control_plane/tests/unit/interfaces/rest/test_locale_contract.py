# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""HTTP-level regressions for the language contract.

These run against the application create_app() actually builds, not a stripped
down one: the middleware order (request logging outside, GZip inside) and the
fact that the catch-all handler lives in ServerErrorMiddleware are exactly what
this contract depends on.
"""

import re
from unittest.mock import patch

import pytest
from starlette.testclient import TestClient

from src.interfaces.rest.main import create_app
from src.shared.errors.domain import NotFoundError
from src.shared.i18n import message

CHINESE = re.compile(r"[一-鿿]")


@pytest.fixture
def client():
    with patch("src.interfaces.rest.main.lifespan"):
        app = create_app()

    @app.get("/api/v1/probe/missing")
    async def _missing():
        raise NotFoundError(message("Sandbox.Session.NotFound", session_id="s-1"))

    @app.get("/api/v1/probe/boom")
    async def _boom():
        raise RuntimeError("downstream is unreachable")

    @app.get("/api/v1/probe/ok")
    async def _ok():
        # Large enough that GZipMiddleware actually compresses it, so the header
        # pass is exercised against an encoded body.
        return {"status": "ok", "padding": "x" * 4000}

    return TestClient(app, raise_server_exceptions=False)


def test_middleware_order_keeps_locale_outside_gzip(client):
    """Locale must wrap GZip, otherwise the header pass would run before the
    body is encoded and the 200 case below would not prove anything."""
    names = [mw.cls.__name__ for mw in client.app.user_middleware]
    assert names.index("BaseHTTPMiddleware") < names.index("GZipMiddleware")


def test_domain_error_follows_the_request_language(client):
    en = client.get("/api/v1/probe/missing", headers={"Accept-Language": "en-US"})
    zh = client.get("/api/v1/probe/missing", headers={"Accept-Language": "zh-CN"})

    assert en.status_code == zh.status_code == 404
    # "error" is the stable machine field and never varies by language.
    assert en.json()["error"] == zh.json()["error"] == "Not Found"
    assert en.json()["message"] == "Session not found: s-1"
    assert CHINESE.search(zh.json()["message"])
    # The identifier survives translation verbatim.
    assert "s-1" in zh.json()["message"]


def test_no_header_keeps_english(client):
    """A caller that states no preference must keep seeing what it saw before
    this service learned to negotiate."""
    response = client.get("/api/v1/probe/missing")
    assert response.json()["message"] == "Session not found: s-1"
    assert response.headers["Content-Language"] == "en-US"


def test_error_response_declares_its_language_and_stays_private(client):
    response = client.get("/api/v1/probe/missing", headers={"Accept-Language": "zh-CN"})
    assert response.headers["Content-Language"] == "zh-CN"
    assert "private" in response.headers["Cache-Control"]
    assert "public" not in response.headers["Cache-Control"]


def test_success_response_is_not_labelled_with_a_language(client):
    """A machine-readable success body carries no localized text, so it gets no
    Content-Language even though it is compressed and cached privately."""
    response = client.get("/api/v1/probe/ok", headers={"Accept-Language": "zh-CN"})
    assert response.status_code == 200
    assert "Content-Language" not in response.headers
    assert "private" in response.headers["Cache-Control"]


def test_unhandled_exception_keeps_the_negotiated_locale(client):
    """The catch-all handler runs in ServerErrorMiddleware, outside the locale
    middleware, so the ContextVar is already reset by the time it renders."""
    response = client.get("/api/v1/probe/boom", headers={"Accept-Language": "zh-CN"})

    assert response.status_code == 500
    body = response.json()
    assert body["error"] == "Internal Server Error"
    assert CHINESE.search(body["message"])
    assert response.headers["Content-Language"] == "zh-CN"
    assert "private" in response.headers["Cache-Control"]


def test_unknown_language_falls_back_without_failing(client):
    response = client.get("/api/v1/probe/missing", headers={"Accept-Language": "kl-KL"})
    assert response.status_code == 404
    assert response.json()["message"] == "Session not found: s-1"
    assert response.headers["Content-Language"] == "en-US"


def test_non_business_path_gets_neither_header(client):
    """The contract is scoped to /api/v1/; the docs and schema routes are not
    authenticated business responses."""
    response = client.get("/openapi.json", headers={"Accept-Language": "zh-CN"})
    assert response.status_code == 200
    assert "Content-Language" not in response.headers
    assert "Cache-Control" not in response.headers
