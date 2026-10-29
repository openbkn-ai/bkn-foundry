# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Official locale migration and display/history/execution agree without editing custom content."""

import asyncio
import json
import re
from contextlib import asynccontextmanager

import pytest
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine

from app import dao
from app.commons import locale
from app.core.prompt import resolve_prompt
from app.models import AgentRow, AgentVersionRow, Base, PromptRow, PromptVersionRow
from app.routers.agent_settings import _shared_prompt, _versions_out
from app.test import test_shared_agent_seeds as seeds


def official_database():
    db = seeds.database()
    seeds.apply(db, seeds.MIGRATIONS / "0.2.1/init.sql")
    return db


def test_official_locale_migration_preserves_versions_and_is_idempotent():
    with official_database() as db:
        before = seeds.snapshot(db)
        seeds.apply(db, seeds.BUILTIN_LOCALES)
        seeds.apply(db, seeds.BUILTIN_LOCALES)
        assert seeds.snapshot(db) == before
        agents = list(
            db.execute(
                "SELECT a.f_agent_id, v.* FROM t_agent a JOIN t_agent_prompt p ON p.f_prompt_id=a.f_prompt_id JOIN t_agent_prompt_version v ON v.f_prompt_id=p.f_prompt_id AND v.f_version=p.f_current_version"
            )
        )
        assert {row["f_agent_id"] for row in agents} == set(seeds.BUILTIN_AGENT_IDS)
        for row in agents:
            variants = json.loads(row["f_localized_content"])
            canonical = row["f_content"]
            for language in ("zh-CN", "en-US"):
                body = variants.get(language, canonical)
                assert body and body != variants.get("other", "")
                assert (
                    bool(re.search(r"[\u4e00-\u9fff]", body))
                    if language == "zh-CN"
                    else body.startswith(("You are", "You help"))
                )
                # Technical identifiers are contractual even inside translated prose.
                identifiers = set(
                    re.findall(r"\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b", canonical)
                )
                assert identifiers <= set(
                    re.findall(r"\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b", body)
                ), (
                    row["f_agent_id"],
                    language,
                    identifiers
                    - set(
                        re.findall(r"\b[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\b", body)
                    ),
                )
            assert variants.get("zh-CN", canonical) != variants.get("en-US", canonical)
        resource = next(
            row
            for row in agents
            if row["f_agent_id"] == "resource-semantic-understanding"
        )
        assert "api_name rules" in json.loads(resource["f_localized_content"])["en-US"]


def test_locale_delta_does_not_touch_custom_content_or_existing_translations():
    with official_database() as db:
        db.execute("UPDATE t_agent_prompt_version SET f_localized_content=NULL")
        # Every official body may be published by a human; matching text alone is insufficient.
        db.execute(
            "UPDATE t_agent_prompt_version SET f_create_user='administrator' WHERE f_prompt_id='studio_smart_qa_prompt'"
        )
        db.execute(
            "UPDATE t_agent_prompt_version SET f_content='Administrator custom prompt' WHERE f_prompt_id='cypher_generation_prompt'"
        )
        db.execute(
            "UPDATE t_agent_prompt_version SET f_localized_content=? WHERE f_prompt_id='graph_explorer_prompt'",
            (json.dumps({"en-US": "Custom translation"}),),
        )
        before = seeds.snapshot(db)
        seeds.apply(db, seeds.BUILTIN_LOCALES)
        after = seeds.snapshot(db)
        assert before[:2] == after[:2] and before[3] == after[3]
        for prompt_id in (
            "studio_smart_qa_prompt",
            "cypher_generation_prompt",
            "graph_explorer_prompt",
        ):
            assert [r for r in before[2] if r[0] == prompt_id] == [
                r for r in after[2] if r[0] == prompt_id
            ]
        assert before != after  # Untouched official bodies were populated.


@asynccontextmanager
async def localized_database():
    tables = (
        ("t_agent", AgentRow),
        ("t_agent_prompt", PromptRow),
        ("t_agent_prompt_version", PromptVersionRow),
        ("t_agent_version", AgentVersionRow),
    )
    json_fields = {
        "f_tools",
        "f_skills",
        "f_limits",
        "f_prompt_vars_schema",
        "f_vars_schema",
        "f_changes",
        "f_localized_content",
    }
    engine = create_async_engine("sqlite+aiosqlite://")
    factory = async_sessionmaker(engine, expire_on_commit=False)
    async with engine.begin() as connection:
        await connection.run_sync(Base.metadata.create_all)
    async with factory() as session:
        with official_database() as db:
            for name, model in tables:
                for row in db.execute(f"SELECT * FROM {name}"):
                    values = dict(row)
                    for field in json_fields & values.keys():
                        if values[field] is not None:
                            values[field] = json.loads(values[field])
                    session.add(model(**values))
        await session.commit()
        try:
            yield session
        finally:
            await session.close()
    await engine.dispose()


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
def test_all_builtin_display_history_and_runtime_share_the_same_variant(language):
    async def run():
        async with localized_database() as session:
            token = locale.set_effective_locale(language)
            try:
                for agent_id in seeds.BUILTIN_AGENT_IDS:
                    agent = await dao.get_agent(session, agent_id)
                    shared = await _shared_prompt(session, agent)
                    runtime, _, version = await resolve_prompt(
                        session, agent, "user", None, {}, shared_only=True
                    )
                    history = await _versions_out(session, agent)
                    assert shared.prompt.content == runtime
                    assert all(
                        item.prompt_content == runtime
                        for item in history
                        if item.prompt_version == version
                    )
                    assert runtime
                    # Canonical export/management reads stay language-independent.
                    canonical = await dao.get_prompt(session, agent.prompt_id)
                    assert canonical.current_version == version
            finally:
                locale.reset_effective_locale(token)

    asyncio.run(run())


@pytest.mark.parametrize(
    "language, expected",
    [("zh-CN", "官方中文正文"), ("en-US", "Official English body")],
)
def test_negotiated_prompt_reaches_the_actual_model_messages(
    monkeypatch, language, expected
):
    from langchain_core.messages import AIMessageChunk, SystemMessage
    from langchain_core.outputs import ChatGenerationChunk
    from langchain_openai import ChatOpenAI
    from langgraph.checkpoint.memory import MemorySaver

    from app.core import graph
    from app.test.test_public_execution import AUTH, BODY, URL, environment, parse_sse

    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            async with factory() as session:
                version = await session.get(PromptVersionRow, ("qa-prompt", 1))
                version.f_content = "官方中文正文"
                version.f_localized_content = {"en-US": "Official English body"}
                await session.commit()
            seen = []

            @asynccontextmanager
            async def checkpoint():
                yield MemorySaver()

            async def provider(self, messages, *args, **kwargs):
                seen.extend(
                    message.content
                    for message in messages
                    if isinstance(message, SystemMessage)
                )
                yield ChatGenerationChunk(message=AIMessageChunk(content="Answer"))

            monkeypatch.setattr(graph, "open_checkpointer", checkpoint)
            monkeypatch.setattr(ChatOpenAI, "_astream", provider)
            response = await client.post(
                URL, json=BODY, headers={**AUTH, "Accept-Language": language}
            )
            assert parse_sse(response.text)[-1][1]["status"] == "succeeded", (
                response.text
            )
            assert seen and all(expected in message for message in seen)

    asyncio.run(run())


@pytest.mark.parametrize("language", ["zh-CN", "en-US"])
def test_custom_publication_is_global_and_activation_restores_official_translations(
    language,
):
    async def run():
        async with localized_database() as session:
            token = locale.set_effective_locale(language)
            try:
                for agent_id in seeds.BUILTIN_AGENT_IDS:
                    agent = await dao.get_agent(session, agent_id)
                    official = (await _shared_prompt(session, agent)).prompt.content
                    initial = next(
                        item
                        for item in await dao.list_versions(session, agent_id)
                        if item.f_initial
                    )
                    await dao.publish_prompt_version(
                        session,
                        agent.prompt_id,
                        "Administrator exact content",
                        None,
                        "admin",
                    )
                    for selected in ("zh-CN", "en-US"):
                        inner = locale.set_effective_locale(selected)
                        try:
                            assert (
                                await _shared_prompt(session, agent)
                            ).prompt.content == "Administrator exact content"
                        finally:
                            locale.reset_effective_locale(inner)
                    await dao.activate_version(
                        session, agent_id, initial.f_version, "admin"
                    )
                    assert (
                        await _shared_prompt(session, agent)
                    ).prompt.content == official
                    assert (
                        await resolve_prompt(
                            session, agent, "user", None, {}, shared_only=True
                        )
                    )[0] == official
            finally:
                locale.reset_effective_locale(token)

    asyncio.run(run())


def test_older_official_resource_versions_receive_exact_matching_translations():
    with seeds.database() as db:
        seeds.apply(db, seeds.MIGRATIONS / "0.2.0/init.sql")
        seeds.apply(db, seeds.CLAIM_LOCALES)
        seeds.apply(db, seeds.SEMANTIC_LANGUAGE)
        before = seeds.snapshot(db)
        seeds.apply(db, seeds.BUILTIN_LOCALES)
        after = seeds.snapshot(db)
        assert before[:2] == after[:2] and before[3] == after[3]
        versions = list(
            db.execute(
                "SELECT f_version, f_content, f_localized_content FROM t_agent_prompt_version WHERE f_prompt_id='resource-semantic-understanding-prompt'"
            )
        )
        assert {row[0] for row in versions} == {3, 4}
        for version, canonical, localized in versions:
            variants = json.loads(localized)
            assert variants["zh-CN"] == canonical
            assert "api_name rules" not in variants["en-US"]
            assert ("Output language rules" in variants["en-US"]) is (version == 4)
