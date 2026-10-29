# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""One explicit target Prompt version per logical publication, including retries."""

import asyncio

import pytest

from app import dao
from app.test.test_agent_settings import ADMIN
from app.test.test_public_execution import AUTH, environment

PUBLICATIONS = f"{ADMIN}/prompt/versions"


@pytest.mark.parametrize("fields", [{}, {"version": None}, {"version": ""}, {"version": 0}, {"version": -1}, {"version": True}, {"version": 2.5}, {"version": "2"}])
def test_publication_requires_a_positive_integer_version(monkeypatch, fields):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            response = await client.post(PUBLICATIONS, json={"content": "New", **fields}, headers=AUTH)
            assert response.status_code == 400, response.text
            async with factory() as session:
                assert len(await dao.list_prompt_versions(session, "qa-prompt")) == 1
                assert await dao.list_versions(session, "studio_smart_qa") == []

    asyncio.run(run())


def test_retries_and_concurrent_edits_cannot_allocate_another_version(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            assert (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()["next_version"] == 2
            body = {"content": "Edited", "version": 2}
            first = await client.post(PUBLICATIONS, json=body, headers=AUTH)
            assert first.status_code == 200, first.text
            assert first.json()["prompt"]["current_version"] == 2
            assert first.json()["next_version"] == 3
            before = (await client.get(f"{ADMIN}/versions", headers=AUTH)).json()
            for content in ("Edited", "Another administrator's edit"):
                retry = await client.post(PUBLICATIONS, json={**body, "content": content}, headers=AUTH)
                assert retry.status_code == 409, retry.text
                assert retry.json()["code"] == "BknAgent.Prompt.VersionConflict"
            assert (await client.get(f"{ADMIN}/versions", headers=AUTH)).json() == before
            async with factory() as session:
                history = await dao.list_prompt_versions(session, "qa-prompt")
            assert [(v.version, v.content) for v in history] == [(2, "Edited"), (1, "Shared instructions")]
            # A genuinely new publication can deliberately reuse the same text.
            next_publish = await client.post(PUBLICATIONS, json={**body, "version": 3}, headers=AUTH)
            assert next_publish.status_code == 200, next_publish.text
            assert next_publish.json()["prompt"]["current_version"] == 3

    asyncio.run(run())


def test_publication_after_rollback_uses_maximum_historical_version(monkeypatch):
    async def run():
        async with environment(monkeypatch) as (client, _, _):
            for version in (2, 3):
                result = await client.post(PUBLICATIONS, json={"content": f"Body {version}", "version": version}, headers=AUTH)
                assert result.status_code == 200, result.text
            switched = await client.post(f"{ADMIN}/versions/1/activate", headers=AUTH)
            assert switched.status_code == 200, switched.text
            restored = (await client.get(f"{ADMIN}/prompt", headers=AUTH)).json()
            assert restored["prompt"]["current_version"] == 1
            assert restored["next_version"] == 4
            skipped = await client.post(PUBLICATIONS, json={"content": "Invalid jump", "version": 5}, headers=AUTH)
            assert skipped.status_code == 409
            published = await client.post(PUBLICATIONS, json={"content": "After rollback", "version": 4}, headers=AUTH)
            assert published.status_code == 200, published.text
            assert published.json()["prompt"]["current_version"] == 4

    asyncio.run(run())


def test_versioned_publication_preserves_schema_and_checks_admin_rights(monkeypatch):
    from app.test.test_agent_settings import _deny_agent_admin

    async def run():
        async with environment(monkeypatch) as (client, factory, _):
            schema = {"type": "object", "required": ["question"]}
            # Existing publication clients remain compatible.
            old = await client.put(f"{ADMIN}/prompt", json={"content": "With schema", "vars_schema": schema}, headers=AUTH)
            assert old.status_code == 200, old.text
            published = await client.post(PUBLICATIONS, json={"content": "Edited", "version": 3}, headers=AUTH)
            assert published.status_code == 200, published.text
            assert published.json()["prompt"]["vars_schema"] == schema
            unauthenticated = await client.post(PUBLICATIONS, json={"content": "Denied", "version": 4})
            assert unauthenticated.status_code == 401
            _deny_agent_admin(monkeypatch)
            denied = await client.post(PUBLICATIONS, json={"content": "Denied", "version": 4}, headers=AUTH)
            assert denied.status_code == 403
            async with factory() as session:
                assert (await dao.get_prompt(session, "qa-prompt")).current_version == 3

    asyncio.run(run())
