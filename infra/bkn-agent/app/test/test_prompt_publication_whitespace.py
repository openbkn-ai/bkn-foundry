# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Shared publication validation follows the Studio String.prototype.trim rule."""

import asyncio

import pytest

from app import dao
from app.models import PromptPublish
from app.test.test_agent_settings import ADMIN
from app.test.test_public_execution import AUTH, environment

# ECMAScript WhiteSpace + LineTerminator (String.prototype.trim), including BOM.
JS_WHITESPACE = (
    "\u0009\u000a\u000b\u000c\u000d\u0020\u00a0\u1680"
    "\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a"
    "\u2028\u2029\u202f\u205f\u3000\ufeff"
)


async def publish(client, versioned, content):
    if versioned:
        return await client.post(
            f"{ADMIN}/prompt/versions",
            json={"content": content, "version": 2},
            headers=AUTH,
        )
    return await client.put(f"{ADMIN}/prompt", json={"content": content}, headers=AUTH)


@pytest.mark.parametrize("versioned", [False, True])
def test_blank_shared_publication_does_not_change_prompts_or_agents(monkeypatch, versioned):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            before_prompt = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()
            before_agent = (await client.get(ADMIN, headers=AUTH)).json()
            for content in ("", *JS_WHITESPACE, " \n\t ", JS_WHITESPACE * 2):
                response = await publish(client, versioned, content)
                assert response.status_code == 400, (repr(content), response.text)
                assert response.json()["code"] == "BknAgent.ParamError.FormatError"
                assert "content" in response.json()["detail"]
                assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json() == before_prompt
                assert (await client.get(ADMIN, headers=AUTH)).json() == before_agent
                async with factory() as session:
                    history = await dao.list_prompt_versions(session, "qa-prompt")
                    assert [(v.version, v.content) for v in history] == [(1, "Shared instructions")]
                    assert await dao.list_versions(session, "studio_smart_qa") == []

    asyncio.run(run())


@pytest.mark.parametrize("versioned", [False, True])
@pytest.mark.parametrize("content", ["Answer", " \n\t你好 {question}\n\ufeff ", "\u0085", "\u001c", "\u200b"])
def test_non_blank_publication_keeps_original_format(monkeypatch, versioned, content):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            response = await publish(client, versioned, content)
            assert response.status_code == 200, response.text
            assert response.json()["prompt"]["content"] == content
            assert response.json()["prompt"]["current_version"] == 2
            assert response.json()["next_version"] == 3
            async with factory() as session:
                history = await dao.list_prompt_versions(session, "qa-prompt")
                assert [(v.version, v.content) for v in history] == [(2, content), (1, "Shared instructions")]
                assert len(await dao.list_versions(session, "studio_smart_qa")) == 2

    asyncio.run(run())


def test_internal_prompt_publication_keeps_its_existing_contract():
    assert PromptPublish(content="\t").content == "\t"
