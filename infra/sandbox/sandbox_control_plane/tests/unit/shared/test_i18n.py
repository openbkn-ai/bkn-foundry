# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Locale negotiation and message localization regressions."""

import re
import subprocess
import sys
from pathlib import Path

import pytest

from src.shared import locale
from src.shared.i18n import en_us, message, zh_cn

CHINESE = re.compile(r"[一-鿿]")


# --- Accept-Language resolution ---------------------------------------------


@pytest.mark.parametrize(
    "header,expected",
    [
        ("en-US", locale.AMERICAN_ENGLISH),
        ("en", locale.AMERICAN_ENGLISH),
        ("zh-CN", locale.SIMPLIFIED_CHINESE),
        ("zh_CN", locale.SIMPLIFIED_CHINESE),
        ("zh-Hans", locale.SIMPLIFIED_CHINESE),
        ("fr, zh-CN;q=0.8", locale.SIMPLIFIED_CHINESE),
        ("zh-CN;q=0.3, en-US;q=0.9", locale.AMERICAN_ENGLISH),
        ("en-US;q=0, zh-CN", locale.SIMPLIFIED_CHINESE),
        ("zh;q=0, zh;q=1", locale.SIMPLIFIED_CHINESE),
        ("zh-CN;q=bad", locale.DEFAULT_LOCALE),
        ("klingon", locale.DEFAULT_LOCALE),
    ],
)
def test_resolve_accept_language(header, expected):
    assert locale.resolve_accept_language(header) == expected


@pytest.mark.parametrize("header", ["", None, "*"])
def test_absent_or_wildcard_header_keeps_the_service_default(header):
    """No stated preference must keep answering English.

    This service shipped English-only messages, so defaulting to the platform
    zh-CN would silently change what every existing caller sees.
    """
    assert locale.resolve_accept_language(header) == locale.AMERICAN_ENGLISH


@pytest.mark.parametrize("header", ["zh-TW", "zh-Hant", "zh-HK", "zh-MO"])
def test_traditional_chinese_is_never_matched_as_simplified(header):
    assert locale._normalize_language(header.lower()) is None


def test_wildcard_does_not_select_a_rejected_locale():
    assert locale.resolve_accept_language("en-US;q=0, *") == locale.SIMPLIFIED_CHINESE


def test_internal_request_headers_carry_one_normalized_locale():
    token = locale.set_effective_locale(locale.SIMPLIFIED_CHINESE)
    try:
        headers = locale.internal_request_headers({"X-Trace": "t"})
    finally:
        locale.reset_effective_locale(token)
    assert headers["Accept-Language"] == locale.SIMPLIFIED_CHINESE
    assert headers["X-Trace"] == "t"


def test_merge_cache_control_keeps_authenticated_responses_private():
    merged = locale.merge_cache_control("public, max-age=60")
    assert "public" not in merged
    assert "private" in merged
    assert "max-age=60" in merged


# --- Catalog integrity ------------------------------------------------------


def test_catalogs_share_keys_and_fields():
    assert set(zh_cn.error_messages) == set(en_us.error_messages)
    for key, entry in zh_cn.error_messages.items():
        assert set(entry) == set(en_us.error_messages[key]), key


def test_english_catalog_has_no_chinese():
    for key, entry in en_us.error_messages.items():
        for field, value in entry.items():
            assert not CHINESE.search(value), f"{key}.{field}"


def test_every_catalog_entry_is_reachable_from_the_code():
    """A key nobody raises is dead weight; a raised key that is missing is a bug."""
    root = Path(__file__).resolve().parents[3] / "src"
    sources = "\n".join(
        path.read_text(encoding="utf-8")
        for path in root.rglob("*.py")
        if "i18n" not in path.parts
    )
    for key in en_us.error_messages:
        assert f'"{key}"' in sources, f"catalog key {key} is never used"


def test_repository_catalog_validation_passes():
    """Run the shared CI validator so a catalog drift fails here too."""
    script = Path(__file__).resolve().parents[6] / "tools" / "check_i18n_catalogs.py"
    result = subprocess.run([sys.executable, str(script)], capture_output=True, text=True)
    assert result.returncode == 0, result.stderr


# --- Message rendering ------------------------------------------------------


def test_message_localizes_wording_and_keeps_identifiers():
    en = message("Sandbox.Session.NotFound", locale="en-US", session_id="s-1")
    zh = message("Sandbox.Session.NotFound", locale="zh-CN", session_id="s-1")
    assert en == "Session not found: s-1"
    assert en != zh
    assert CHINESE.search(zh)
    # The identifier is a machine value and survives translation verbatim.
    assert "s-1" in en and "s-1" in zh


def test_missing_key_degrades_to_the_key_instead_of_raising():
    assert message("Sandbox.DoesNotExist", locale="en-US") == "Sandbox.DoesNotExist"


def test_broken_template_falls_back_to_plain_wording():
    rendered = message("Sandbox.Session.NotFound", locale="en-US")
    assert rendered == en_us.error_messages["Sandbox.Session.NotFound"]["message"]


def test_message_follows_the_frozen_request_locale():
    token = locale.set_effective_locale(locale.SIMPLIFIED_CHINESE)
    try:
        rendered = message("Sandbox.Template.NotFound", template_id="t-1")
    finally:
        locale.reset_effective_locale(token)
    assert CHINESE.search(rendered)
    assert "t-1" in rendered
