# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Database-backed prompt locales preserve caller and administrator overrides."""

import asyncio
from contextlib import asynccontextmanager

import pytest
from sqlalchemy import event
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app import dao
from app.auth import Account
from app.commons import locale
from app.core.prompt import resolve_prompt
from app.models import AgentRow, Base, PromptRow, PromptVersionRow
from app.routers.agent_settings import _shared_prompt
from app.routers.prompts import get_effective_prompt

AGENT = "business_provenance_claim_attribution"
PROMPT = "business_provenance_claim_attribution_prompt"
ZH = "官方中文提示词"
EN = "Official English prompt"


@asynccontextmanager
async def database():
    engine = create_async_engine("sqlite+aiosqlite://")
    factory = async_sessionmaker(engine, expire_on_commit=False)
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)
    async with factory() as session:
        session.add_all([
            AgentRow(f_agent_id=AGENT, f_name="Claims", f_mode="chat", f_prompt_id=PROMPT,
                     f_status="published", f_create_user="seed", f_update_user="seed",
                     f_create_time=1, f_update_time=1),
            PromptRow(f_prompt_id=PROMPT, f_name="Claims prompt", f_current_version=1,
                      f_update_user="seed", f_update_time=1),
            PromptVersionRow(f_prompt_id=PROMPT, f_version=1, f_content=ZH,
                             f_localized_content={"en-US": EN}, f_create_user="seed", f_create_time=1),
        ])
        await session.commit()
        try:
            yield session, await dao.get_agent(session, AGENT)
        finally:
            await session.close()
    await engine.dispose()


@pytest.mark.parametrize("language, expected", [("zh-CN", ZH), ("en-US", EN)])
def test_display_and_execution_select_same_database_variant(language, expected):
    async def run():
        async with database() as (session, agent):
            token = locale.set_effective_locale(language)
            try:
                result = await resolve_prompt(session, agent, "user", None, {}, shared_only=True)
                effective = await get_effective_prompt(AGENT, Account("user", "user"), session)
                shared = await _shared_prompt(session, agent)
                assert result == (expected, "default", 1)
                assert effective.content == shared.prompt.content == expected
                # Export, editing history, and snapshots keep the canonical body.
                assert (await dao.get_prompt(session, PROMPT)).content == ZH
                assert (await dao.list_prompt_versions(session, PROMPT))[0].content == ZH
            finally:
                locale.reset_effective_locale(token)
    asyncio.run(run())


def test_overrides_and_custom_versions_are_not_replaced_by_translations():
    async def run():
        async with database() as (session, agent):
            token = locale.set_effective_locale("en-US")
            try:
                await dao.set_prompt_override(session, AGENT, "user", "Personal prompt")
                assert (await resolve_prompt(session, agent, "user", "Request prompt", {}))[:2] == ("Request prompt", "request")
                assert (await resolve_prompt(session, agent, "user", None, {}))[:2] == ("Personal prompt", "override")
                assert (await get_effective_prompt(AGENT, Account("user", "user"), session)).content == "Personal prompt"
                assert (await resolve_prompt(session, agent, "user", "Request prompt", {}, shared_only=True))[0] == EN
                await dao.publish_prompt_version(session, PROMPT, "Admin custom prompt", None, "admin")
                for language in ("en-US", "zh-CN"):
                    inner = locale.set_effective_locale(language)
                    try:
                        assert (await resolve_prompt(session, agent, "user", None, {}, shared_only=True))[0] == "Admin custom prompt"
                        assert (await _shared_prompt(session, agent)).prompt.content == "Admin custom prompt"
                    finally:
                        locale.reset_effective_locale(inner)
                await dao.rollback_prompt(session, PROMPT, 1, "admin")
                assert (await resolve_prompt(session, agent, "user", None, {}, shared_only=True))[0] == EN
            finally:
                locale.reset_effective_locale(token)
    asyncio.run(run())


def test_untranslated_prompts_and_missing_locale_fall_back_to_canonical():
    async def run():
        async with database() as (session, agent):
            version = await session.get(PromptVersionRow, (PROMPT, 1))
            version.f_localized_content = None
            await session.commit()
            token = locale.set_effective_locale("en-US")
            try:
                assert (await resolve_prompt(session, agent, "user", None, {}))[0] == ZH
            finally:
                locale.reset_effective_locale(token)
    asyncio.run(run())


def test_locale_selection_keeps_the_existing_two_select_query_budget():
    async def run():
        async with database() as (session, agent):
            queries = []
            def capture(connection, cursor, statement, parameters, context, executemany):
                queries.append(statement)
            event.listen(session.bind.sync_engine, "before_cursor_execute", capture)
            for language in ("zh-CN", "en-US"):
                token = locale.set_effective_locale(language)
                try:
                    session.expunge_all()
                    queries.clear()
                    await resolve_prompt(session, agent, "user", None, {}, shared_only=True)
                    assert len(queries) == 2
                    session.expunge_all()
                    queries.clear()
                    await dao.get_prompt(session, PROMPT, localized=True)
                    assert len(queries) == 2
                finally:
                    locale.reset_effective_locale(token)
    asyncio.run(run())
