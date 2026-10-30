# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Manual per-version translations, original language, permission and version safety."""

import asyncio
from unittest.mock import AsyncMock

import pytest
from sqlalchemy import func, select

from app import dao
from app.models import AgentVersionRow, PromptVersionRow
from app.routers import agent_settings
from app.test.test_public_execution import AUTH, environment

ADMIN = "/api/bkn-agent/v1/admin/agents/studio_smart_qa"


async def counts(factory):
    async with factory() as session:
        result = []
        for table in (PromptVersionRow, AgentVersionRow):
            result.append(
                (
                    await session.execute(select(func.count()).select_from(table))
                ).scalar_one()
            )
        return tuple(result)


async def publish(client, content="中文原文", language="zh-CN"):
    before = (await client.get(ADMIN + "/prompt", headers=AUTH)).json()
    response = await client.post(
        ADMIN + "/prompt/versions",
        json={
            "content": content,
            "source_language": language,
            "version": before["next_version"],
        },
        headers=AUTH,
    )
    assert response.status_code == 200, response.text
    return response.json()


def body(original, content="English translation"):
    return {
        "content": content,
        "source_language": original["source_language"] or "zh-CN",
        "source_content_sha256": original["source_content_sha256"],
    }


def translation_url(original, language="en-US"):
    return (
        ADMIN
        + f"/prompt/versions/{original['prompt']['current_version']}/translations/{language}"
    )


def test_add_edit_translation_preserves_original_versions_and_pointers(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            original = await publish(client)
            assert (
                original["source_language"] == "zh-CN"
                and original["translations"] == {}
            )
            before = await counts(factory)
            for content in ("English translation", "Revised English translation"):
                response = await client.put(
                    translation_url(original),
                    json=body(original, content),
                    headers={**AUTH, "Accept-Language": "en-US"},
                )
                assert response.status_code == 200, response.text
                result = response.json()
                assert result["prompt"]["content"] == content
                assert result["source_content"] == "中文原文"
                assert result["content_language"] == "en-US"
                assert (
                    result["prompt"]["current_version"]
                    == original["prompt"]["current_version"]
                )
                assert result["next_version"] == original["next_version"]
                assert await counts(factory) == before
                zh = (
                    await client.get(
                        ADMIN + "/prompt", headers={**AUTH, "Accept-Language": "zh-CN"}
                    )
                ).json()
                assert (
                    zh["prompt"]["content"] == "中文原文"
                    and zh["content_language"] == "zh-CN"
                )
                history = (
                    await client.get(
                        ADMIN + "/versions",
                        headers={**AUTH, "Accept-Language": "en-US"},
                    )
                ).json()
                assert (
                    next(v for v in history if v["current"])["prompt_content"]
                    == content
                )
            async with factory() as session:
                assert (
                    await dao.get_prompt(session, "qa-prompt")
                ).content == "中文原文"
            # A later original starts with no translations even when its predecessor has one.
            newer = await publish(client, "New English original", "en-US")
            assert newer["translations"] == {} and newer["source_language"] == "en-US"
            fallback = (
                await client.get(
                    ADMIN + "/prompt", headers={**AUTH, "Accept-Language": "zh-CN"}
                )
            ).json()
            assert (
                fallback["prompt"]["content"] == "New English original"
                and fallback["content_language"] == "en-US"
            )
            zh = await client.put(
                translation_url(newer, "zh-CN"),
                json=body(newer, "中文译文"),
                headers={**AUTH, "Accept-Language": "zh-CN"},
            )
            assert (
                zh.status_code == 200 and zh.json()["prompt"]["content"] == "中文译文"
            )

    asyncio.run(run())


@pytest.mark.parametrize(
    "change", ["active-version", "original-hash", "source-language"]
)
def test_translation_rejects_stale_original_without_writing(monkeypatch, change):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            original = await publish(client)
            payload = body(original)
            if change == "active-version":
                await publish(client, "Newer original")
            elif change == "original-hash":
                payload["source_content_sha256"] = "0" * 64
            else:
                payload["source_language"] = "en-US"
            before = await counts(factory)
            result = await client.put(
                translation_url(original), json=payload, headers=AUTH
            )
            assert result.status_code == 409, result.text
            assert await counts(factory) == before
            async with factory() as session:
                row = await session.get(
                    PromptVersionRow,
                    ("qa-prompt", original["prompt"]["current_version"]),
                )
                assert (
                    row.f_localized_content is None and row.f_source_language == "zh-CN"
                )

    asyncio.run(run())


@pytest.mark.parametrize(
    "language,content",
    [("zh-CN", "Not original"), ("en-US", " \t\ufeff"), ("fr-FR", "French")],
)
def test_translation_rejects_original_language_blank_or_unsupported_locale(
    monkeypatch, language, content
):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            original = await publish(client)
            before = await counts(factory)
            response = await client.put(
                translation_url(original, language),
                json=body(original, content),
                headers=AUTH,
            )
            assert response.status_code == 400, response.text
            assert await counts(factory) == before

    asyncio.run(run())


def test_legacy_language_requires_explicit_confirmation_and_does_not_add_versions(
    monkeypatch,
):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            original = (await client.get(ADMIN + "/prompt", headers=AUTH)).json()
            assert (
                original["source_language"] is None
                and original["content_language"] is None
            )
            before = await counts(factory)
            payload = body(original)
            missing = await client.put(
                translation_url(original),
                json={k: v for k, v in payload.items() if k != "source_language"},
                headers=AUTH,
            )
            assert missing.status_code == 400
            saved = await client.put(
                translation_url(original), json=payload, headers=AUTH
            )
            assert (
                saved.status_code == 200 and saved.json()["source_language"] == "zh-CN"
            )
            assert await counts(factory) == before

    asyncio.run(run())


def test_translation_requires_existing_admin_permission(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            original = await publish(client)
            before = await counts(factory)
            monkeypatch.setattr(
                agent_settings, "can_manage_agents", AsyncMock(return_value=False)
            )
            denied = await client.put(
                translation_url(original), json=body(original), headers=AUTH
            )
            assert denied.status_code == 403
            anonymous = await client.put(translation_url(original), json=body(original))
            assert anonymous.status_code == 401
            assert await counts(factory) == before

    asyncio.run(run())
