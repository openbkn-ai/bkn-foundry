# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Locale negotiation and error-envelope localization regressions."""

import re
import subprocess
import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from app.commons import locale
from app.commons.i18n import build_error_content, en_us, zh_cn
from app.main import app

client = TestClient(app)
SVC = {"x-account-id": "svc-test", "x-account-type": "app"}
CHINESE = re.compile(r"[一-鿿]")


# --- Accept-Language resolution -------------------------------------------


@pytest.mark.parametrize(
    "header,expected",
    [
        ("en-US", locale.ENGLISH_LOCALE),
        ("en", locale.ENGLISH_LOCALE),
        ("zh-CN", locale.DEFAULT_LOCALE),
        ("zh_CN", locale.DEFAULT_LOCALE),
        ("zh-Hans", locale.DEFAULT_LOCALE),
        ("", locale.DEFAULT_LOCALE),
        (None, locale.DEFAULT_LOCALE),
        ("fr, en-US;q=0.8", locale.ENGLISH_LOCALE),
        ("en-US;q=0.3, zh-CN;q=0.9", locale.DEFAULT_LOCALE),
        ("*", locale.DEFAULT_LOCALE),
        ("zh-CN;q=0, en-US", locale.ENGLISH_LOCALE),
        ("en;q=0, en;q=1", locale.ENGLISH_LOCALE),
        ("en-US;q=bad", locale.DEFAULT_LOCALE),
        ("klingon", locale.DEFAULT_LOCALE),
    ],
)
def test_resolve_accept_language(header, expected):
    assert locale.resolve_accept_language(header) == expected


@pytest.mark.parametrize("header", ["zh-TW", "zh-Hant", "zh-HK", "zh-MO"])
def test_traditional_chinese_is_never_matched_as_simplified(header):
    """Traditional tags must not be *matched* to zh-CN.

    They are unsupported, so the service default applies — but the tag itself
    must never resolve as a Simplified Chinese match, otherwise adding a
    zh-Hant catalog later would silently keep serving Simplified text.
    """
    assert locale._normalize_language(header.lower()) is None


def test_wildcard_does_not_select_a_rejected_locale():
    assert locale.resolve_accept_language("zh-CN;q=0, *") == locale.ENGLISH_LOCALE


def test_internal_request_headers_carry_one_normalized_locale():
    token = locale.set_effective_locale(locale.ENGLISH_LOCALE)
    try:
        headers = locale.internal_request_headers({"Authorization": "Bearer x"})
    finally:
        locale.reset_effective_locale(token)
    assert headers["Accept-Language"] == locale.ENGLISH_LOCALE
    assert headers["Authorization"] == "Bearer x"


# --- Catalog integrity ------------------------------------------------------


def test_catalogs_share_keys_codes_and_fields():
    assert set(zh_cn.error_messages) == set(en_us.error_messages)
    assert set(zh_cn.resource_names) == set(en_us.resource_names)
    for key, message in zh_cn.error_messages.items():
        translated = en_us.error_messages[key]
        assert set(message) == set(translated), key
        # A standalone message (an import warning) carries no public code.
        if "code" in message:
            assert message["code"] == translated["code"], key


def test_english_catalog_has_no_chinese():
    for key, message in en_us.error_messages.items():
        for field, value in message.items():
            assert not CHINESE.search(value), f"{key}.{field}"
    for key, value in en_us.resource_names.items():
        assert not CHINESE.search(value), key


def test_repository_catalog_validation_passes():
    """Run the shared CI validator so a catalog drift fails here too."""
    script = Path(__file__).resolve().parents[4] / "tools" / "check_i18n_catalogs.py"
    result = subprocess.run(
        [sys.executable, str(script)], capture_output=True, text=True
    )
    assert result.returncode == 0, result.stderr


# --- Envelope rendering -----------------------------------------------------


def test_build_error_content_keeps_code_and_localizes_text():
    zh = build_error_content(
        "BknAgent.Chat.ModeMismatch", locale="zh-CN", agent_id="a1", mode="task"
    )
    en = build_error_content(
        "BknAgent.Chat.ModeMismatch", locale="en-US", agent_id="a1", mode="task"
    )
    assert zh["code"] == en["code"] == "BknAgent.ParamError.Mode"
    assert zh["description"] != en["description"]
    assert not CHINESE.search(en["description"])
    assert zh["detail"] == en["detail"] == "agent a1 mode=task"


def test_shared_code_can_carry_different_wording():
    conflict = build_error_content("BknAgent.Agent.Conflict", locale="en-US", name="n", agent_id="a")
    name_only = build_error_content("BknAgent.Agent.NameConflict", locale="en-US", name="n")
    assert conflict["code"] == name_only["code"] == "BknAgent.ParamError.Conflict"
    assert conflict["description"] != name_only["description"]


def test_missing_key_degrades_instead_of_raising():
    content = build_error_content("BknAgent.DoesNotExist", locale="en-US")
    assert content["code"] == "BknAgent.DoesNotExist"
    assert content["description"] == en_us.error_messages["BknAgent.Internal.Unexpected"]["description"]


def test_broken_template_falls_back_to_plain_text():
    content = build_error_content("BknAgent.Thread.Busy", locale="en-US", unexpected="x")
    assert content["detail"] == en_us.error_messages["BknAgent.Thread.Busy"]["detail"]


def test_placeholder_named_status_does_not_collide_with_err_signature():
    from app.errors import err

    exception = err(502, "BknAgent.Toolbox.ListFailed", box_id="b", status=503, body="boom")
    assert exception.status_code == 502
    assert exception.detail["detail"] == "toolbox b list failed: HTTP 503 boom"


# --- HTTP surface -----------------------------------------------------------


def _unknown_route(language: str | None):
    """404 on a business path — exercises the envelope without touching MySQL."""
    headers = dict(SVC)
    if language is not None:
        headers["Accept-Language"] = language
    return client.get("/api/bkn-agent/v1/no-such-endpoint", headers=headers)


def _invalid_body(language: str):
    headers = dict(SVC)
    headers["Accept-Language"] = language
    return client.post("/api/bkn-agent/v1/agents", json={}, headers=headers)


def test_same_error_keeps_machine_fields_across_locales():
    zh = _invalid_body("zh-CN")
    en = _invalid_body("en-US")
    assert zh.status_code == en.status_code == 400
    assert zh.json()["code"] == en.json()["code"] == "BknAgent.ParamError.FormatError"
    assert CHINESE.search(zh.json()["description"])
    assert not CHINESE.search(en.json()["description"])
    # The field-level detail is pydantic machine text: identical in both locales.
    assert zh.json()["detail"] == en.json()["detail"]


def test_error_response_declares_its_language_and_stays_private():
    response = _invalid_body("en-US")
    assert response.headers["Content-Language"] == "en-US"
    assert "private" in response.headers["Cache-Control"]
    assert "public" not in response.headers["Cache-Control"]


def test_unknown_language_falls_back_without_failing():
    response = _unknown_route("kl-KL")
    assert response.status_code == 404
    assert response.json()["code"] == "BknAgent.Http.404"
    assert response.headers["Content-Language"] == "zh-CN"


def test_framework_404_localizes_description_and_keeps_reason_phrase():
    response = _unknown_route("en-US")
    body = response.json()
    assert body["code"] == "BknAgent.Http.404"
    assert body["detail"] == "Not Found"
    assert not CHINESE.search(body["description"])


def test_auth_error_is_localized_before_identity_is_known():
    response = client.get(
        "/api/bkn-agent/v1/agents/any", headers={"Accept-Language": "en-US"}
    )
    assert response.status_code == 401
    body = response.json()
    assert body["code"] == "BknAgent.Auth.AccountRequired"
    assert not CHINESE.search(body["description"])


def test_health_is_machine_only_and_gets_no_language_header():
    response = client.get("/api/v1/health", headers={"Accept-Language": "en-US"})
    assert response.status_code == 200
    assert "Content-Language" not in response.headers

def test_unhandled_exception_keeps_the_negotiated_locale():
    """The catch-all handler runs outside the middleware, where the ContextVar
    has already been reset; it must still answer in the request's locale."""
    from app.db import get_session

    async def _boom():
        raise RuntimeError("downstream is unreachable")
        yield  # pragma: no cover - never reached

    app.dependency_overrides[get_session] = _boom
    try:
        failing = TestClient(app, raise_server_exceptions=False)
        headers = dict(SVC)
        headers["Accept-Language"] = "en-US"
        response = failing.get("/api/bkn-agent/v1/agents/any", headers=headers)
    finally:
        app.dependency_overrides.pop(get_session, None)

    assert response.status_code == 500
    body = response.json()
    assert body["code"] == "BknAgent.Internal.Unexpected"
    assert not CHINESE.search(body["description"])
    assert not CHINESE.search(body["solution"])
    # The exception path skips the normal header pass, so the handler applies it.
    assert response.headers["Content-Language"] == "en-US"
    assert "private" in response.headers["Cache-Control"]
    # The internal diagnostic stays untranslated.
    assert body["detail"].startswith("RuntimeError:")


def test_downstream_headers_carry_the_frozen_locale():
    """Every downstream call sends one normalized Accept-Language, never the
    caller's raw multi-candidate header."""
    from app.core import skills, toolbox

    token = locale.set_effective_locale(locale.ENGLISH_LOCALE)
    try:
        built = locale.internal_request_headers(
            {"x-account-id": "a", "x-account-type": "user"}
        )
    finally:
        locale.reset_effective_locale(token)
    assert built["Accept-Language"] == "en-US"
    # Guard the wiring itself: these modules must go through the helper.
    for module in (skills, toolbox):
        source = open(module.__file__, encoding="utf-8").read()
        assert "locale.internal_request_headers(" in source, module.__name__
