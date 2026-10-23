# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Actual MariaDB locking/atomicity acceptance; only disposable random DB."""
import asyncio, json, uuid, os
import pytest
from pathlib import Path
import pymysql
from sqlalchemy import select
from sqlalchemy.ext.asyncio import create_async_engine, async_sessionmaker
from app import dao
from app.auth import Account
from app.models import Base, AgentRow, PromptRow, PromptVersionRow, AgentVersionRow, SharedAgentUpdate, PromptPublish
from app.routers import agent_settings as routes
AGENT = 'business_provenance_claim_attribution'
PROMPT = 'fixture'
checks = []

def check(name, value):
    checks.append({'name': name, 'passed': bool(value)})
    assert value, name

async def main(monkeypatch):
    checks.clear()
    name = 'claim_tx_' + uuid.uuid4().hex
    db = pymysql.connect(host=os.environ['BKN_AGENT_TEST_MARIADB_HOST'], port=int(os.getenv('BKN_AGENT_TEST_MARIADB_PORT', '3306')), user='root', password='', autocommit=True)
    with db.cursor() as c:
        c.execute(f'CREATE DATABASE `{name}`')
    engine = create_async_engine(f"mysql+aiomysql://root@{os.environ['BKN_AGENT_TEST_MARIADB_HOST']}:{os.getenv('BKN_AGENT_TEST_MARIADB_PORT', '3306')}/{name}")
    factory = async_sessionmaker(engine, expire_on_commit=False)

    async def admin(_):
        return True
    monkeypatch.setattr(routes, 'can_manage_agents', admin)
    try:
        async with engine.begin() as c:
            await c.run_sync(Base.metadata.create_all)
        async with factory() as s:
            s.add_all([AgentRow(f_agent_id=AGENT, f_name='Fixture', f_mode='chat', f_prompt_id=PROMPT, f_status='published', f_create_user='seed', f_update_user='seed', f_create_time=1, f_update_time=1), PromptRow(f_prompt_id=PROMPT, f_name='Fixture prompt', f_current_version=1, f_update_user='seed', f_update_time=1), PromptVersionRow(f_prompt_id=PROMPT, f_version=1, f_content='canonical', f_create_user='seed', f_create_time=1)])
            await s.commit()
            await dao.record_version(s, AGENT, 'seed')
            await dao.update_shared_agent(s, AGENT, {'model': 'model2'}, 'admin')
            await dao.update_shared_agent(s, AGENT, {'model': 'model3'}, 'admin')
        original = dao.record_version
        for kind in ('runtime', 'prompt'):
            calls = 0

            async def fail_second(*args, **kw):
                nonlocal calls
                calls += 1
                if calls == 2:
                    raise RuntimeError('injected history failure')
                return await original(*args, **kw)
            dao.record_version = fail_second
            try:
                async with factory() as s:
                    try:
                        if kind == 'runtime':
                            await routes.update_shared_agent(SharedAgentUpdate(model='MUST-NOT-COMMIT'), AGENT, Account('admin', 'user'), s)
                        else:
                            await routes.publish_shared_prompt(PromptPublish(content='MUST-NOT-COMMIT'), AGENT, Account('admin', 'user'), s)
                    except RuntimeError:
                        pass
                    else:
                        raise AssertionError('injection did not run')
            finally:
                dao.record_version = original
            async with factory() as s:
                agent = await dao.get_agent(s, AGENT)
                prompt = await dao.get_prompt(s, PROMPT)
                versions = await dao.list_versions(s, AGENT)
                check(kind + '-history-failure-rolls-back-business', agent.model == 'model3' and prompt.current_version == 1 and (prompt.content == 'canonical') and all((v.f_model != 'MUST-NOT-COMMIT' for v in versions)))
        async with factory() as a, factory() as b:
            stale = await b.get(AgentVersionRow, (AGENT, 2))
            assert not stale.f_current
            await dao.activate_version(a, AGENT, 2, 'admin', commit=False)
            pending = asyncio.create_task(dao.delete_version(b, AGENT, 2, commit=False))
            await asyncio.sleep(0.15)
            check('delete-waits-for-activate-agent-lock', not pending.done())
            await a.commit()
            result = await asyncio.wait_for(pending, 3)
            check('cached-inactive-version-refreshed-now-current', result == 'current' and stale.f_current and (not stale.f_deleted))
            await b.rollback()
        async with factory() as a, factory() as b:
            stale = await b.get(AgentVersionRow, (AGENT, 3))
            assert not stale.f_deleted
            assert await dao.delete_version(a, AGENT, 3, commit=False) == 'deleted'
            pending = asyncio.create_task(dao.activate_version(b, AGENT, 3, 'admin', commit=False))
            await asyncio.sleep(0.15)
            check('activate-waits-for-delete-agent-lock', not pending.done())
            await a.commit()
            check('cached-deleted-version-not-activated', await asyncio.wait_for(pending, 3) is None and stale.f_deleted)
            await b.rollback()
        async with factory() as a, factory() as b:
            stale = await b.get(AgentRow, AGENT)
            await dao.update_shared_agent(a, AGENT, {'model': 'writer-A'}, 'admin', commit=False)
            pending = asyncio.create_task(dao.update_shared_agent(b, AGENT, {'model': 'writer-B'}, 'admin', commit=False))
            await asyncio.sleep(0.15)
            check('runtime-writers-serialize', not pending.done())
            await a.commit()
            await asyncio.wait_for(pending, 3)
            await b.commit()
        async with factory() as s:
            versions = await dao.list_versions(s, AGENT)
            check('both-runtime-writes-have-history', {'writer-A', 'writer-B'}.issubset({v.f_model for v in versions}))
            check('one-current-not-deleted', sum((v.f_current for v in versions)) == 1 and next((v for v in versions if v.f_current)).f_model == 'writer-B')

        async def publish(body):
            async with factory() as s:
                return await routes.publish_shared_prompt(PromptPublish(content=body), AGENT, Account('admin', 'user'), s)
        await asyncio.gather(publish('prompt-A'), publish('prompt-B'))
        async with factory() as s:
            prompts = await dao.list_prompt_versions(s, PROMPT)
            versions = await dao.list_versions(s, AGENT)
            check('concurrent-prompt-writes-both-recorded', {p.content for p in prompts} == {'canonical', 'prompt-A', 'prompt-B'} and {2, 3}.issubset({v.f_prompt_version for v in versions}))
            check('concurrent-prompt-single-current', sum((v.f_current for v in versions)) == 1)
    finally:
        await engine.dispose()
        with db.cursor() as c:
            c.execute(f'DROP DATABASE IF EXISTS `{name}`')
        db.close()
    print(json.dumps({'passed': len(checks), 'total': len(checks), 'real_mariadb': True, 'database_dropped': True}))

@pytest.mark.skipif(not os.getenv('BKN_AGENT_TEST_MARIADB_HOST'), reason='isolated MariaDB not configured')
def test_admin_transactions_real_mariadb(monkeypatch):
    asyncio.run(main(monkeypatch))
