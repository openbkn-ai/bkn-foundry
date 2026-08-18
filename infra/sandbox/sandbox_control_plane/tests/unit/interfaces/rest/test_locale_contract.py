# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""HTTP-level regressions for the language contract."""

import re
from unittest.mock import patch

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from src.interfaces.rest.main import _register_exception_handlers, _register_middleware
from src.shared.errors.domain import NotFoundError
from src.shared.i18n import message

CHINESE = re.compile(r"[一-鿿]")


@pytest.fixture
def client():
    """A minimal app carrying only the pieces under test.

    create_app() reaches for the database and the container runtime, which this
    regression does not need; the handlers and middleware are registered the
    same way it registers them.
    """
    app = FastAPI()
    _register_exception_handlers(app)
    _register_middleware(app)

    @app.get("/api/v1/probe/missing")
    async def _missing():
        raise NotFoundError(message("Sandbox.Session.NotFound", session_id="s-1"))

    @app.get("/api/v1/probe/boom")
    async def _boom():
        raise RuntimeError("downstream is unreachable")

    return TestClient(app, raise_server_exceptions=False)


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
    response = client.get("/api/v1/probe/missing")
    assert response.json()["message"] == "Session not found: s-1"


def test_error_response_declares_its_language_and_stays_private(client):
    response = client.get("/api/v1/probe/missing", headers={"Accept-Language": "zh-CN"})
    assert response.headers["Content-Language"] == "zh-CN"
    assert "private" in response.headers["Cache-Control"]
    assert "public" not in response.headers["Cache-Control"]


def test_unhandled_exception_keeps_the_negotiated_locale(client):
    """The catch-all handler runs outside the locale middleware, where the
    ContextVar has already been reset; it must still answer in the request's
    language and still declare it."""
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
