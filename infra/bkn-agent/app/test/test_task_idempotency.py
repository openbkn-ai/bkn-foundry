# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""One-shot asynchronous runs reuse the existing durable task PK boundary."""
import asyncio

from sqlalchemy import select

from app import dao
from app.core import llm, runner
from app.models import AgentRow, TaskRow
from app.test.test_public_execution import environment

URL = "/api/bkn-agent/v1/run"
HEADERS = {
    "x-account-id": "owner", "x-account-type": "app",
    "Idempotency-Key": "capture-1", "Accept-Language": "en-US",
    "x-bkn-agent-thinking-mode": "disabled",
}
BODY = {"agent_id": "studio_smart_qa", "message": "Extract claims"}


async def seed(factory):
    async with factory() as session:
        agent = await session.get(AgentRow, "studio_smart_qa")
        agent.f_mode = "task"
        await session.commit()


def test_run_reuses_pending_and_completed_task_without_resubmission(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            await seed(factory)
            calls = []
            monkeypatch.setattr(runner, "submit_task", lambda *args: calls.append((args[0], llm.current_thinking_mode())))
            first = await client.post(URL, json=BODY, headers=HEADERS)
            again = await client.post(URL, json=BODY, headers=HEADERS)
            assert first.status_code == again.status_code == 200
            task_id = first.json()["task_id"]
            assert first.json()["input"]["request_context"] == {"locale": "en-US", "thinking_mode": "disabled"}
            for changed_headers in [
                {**HEADERS, "Accept-Language": "zh-CN"},
                {**HEADERS, "x-bkn-agent-thinking-mode": "enabled"},
            ]:
                conflict = await client.post(URL, json=BODY, headers=changed_headers)
                assert conflict.status_code == 409
            assert again.json()["task_id"] == task_id
            assert calls == [(task_id, "disabled")]
            async with factory() as session:
                await dao.set_task_status(session, task_id, "succeeded", output='{"claims":[]}')
            replay = await client.post(URL, json=BODY, headers=HEADERS)
            assert replay.json()["status"] == "succeeded"
            assert replay.json()["output"] == '{"claims":[]}'
            assert len(calls) == 1
            changed = await client.post(URL, json={**BODY, "message": "Changed"}, headers=HEADERS)
            assert changed.status_code == 409
            assert changed.json()["code"] == "BknAgent.Task.IdempotencyConflict"
            foreign = await client.get(f"/api/bkn-agent/v1/tasks/{task_id}", headers={"x-account-id": "other", "x-account-type": "app"})
            assert foreign.status_code == 404
    asyncio.run(run())


def test_run_scope_isolates_actor_and_key_and_preserves_old_calls(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            await seed(factory)
            monkeypatch.setattr(runner, "submit_task", lambda *args: None)
            headers = [
                HEADERS, {**HEADERS, "x-account-id": "other"},
                {**HEADERS, "Idempotency-Key": "capture-2"},
                {k: v for k, v in HEADERS.items() if k != "Idempotency-Key"},
            ]
            ids = [(await client.post(URL, json=BODY, headers=h)).json()["task_id"] for h in headers]
            ids.append((await client.post(URL, json=BODY, headers=headers[-1])).json()["task_id"])
            assert len(set(ids)) == 5
            invalid = await client.post(URL, json=BODY, headers={**HEADERS, "Idempotency-Key": ""})
            assert invalid.status_code == 400
    asyncio.run(run())


def test_concurrent_task_reservation_has_one_winner(monkeypatch):
    async def run():
        async with environment(monkeypatch, stream=False) as (_, factory, _):
            async def reserve():
                async with factory() as session:
                    return await dao.create_idempotent_task(
                        session, "a", {"message": "x"}, "owner", "app", "same-key", "hash",
                    )
            a, b = await asyncio.gather(reserve(), reserve())
            assert a[0].task_id == b[0].task_id
            assert int(a[1]) + int(b[1]) == 1
            async with factory() as session:
                rows = (await session.execute(select(TaskRow))).scalars().all()
                assert len(rows) == 1
    asyncio.run(run())


def test_published_chat_agent_runs_asynchronously_and_reuses_terminal_output(monkeypatch):
    from app.test.test_task_versions import wire_runner

    async def run():
        async with environment(monkeypatch, stream=False) as (client, factory, _):
            # Preserve the default chat mode: the claim-attribution seed uses it.
            calls = []
            wire_runner(monkeypatch, factory, calls)
            first = await client.post(URL, json=BODY, headers=HEADERS)
            assert first.status_code == 200
            task_id = first.json()["task_id"]
            for _ in range(100):
                result = await client.get(f"/api/bkn-agent/v1/tasks/{task_id}", headers=HEADERS)
                if result.json()["status"] in ("succeeded", "failed"):
                    break
                await asyncio.sleep(0.01)
            assert result.json()["status"] == "succeeded", result.json()
            assert result.json()["output"] == '{"answer": "ok"}'
            again = await client.post(URL, json=BODY, headers=HEADERS)
            assert again.json()["task_id"] == task_id
            assert again.json()["status"] == "succeeded"
            assert len([call for call in calls if call[0] == "config"]) == 1
            async with factory() as session:
                agent = await session.get(AgentRow, BODY["agent_id"])
                agent.f_status = "draft"
                await session.commit()
            assert (await client.post(URL, json=BODY, headers=HEADERS)).status_code == 404
    asyncio.run(run())
