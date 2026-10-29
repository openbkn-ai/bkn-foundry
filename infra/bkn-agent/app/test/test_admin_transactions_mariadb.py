# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Actual MariaDB locking/atomicity acceptance; only disposable random DB."""
import asyncio, json, uuid, os
import pytest
from pathlib import Path
import pymysql
from sqlalchemy import select, text
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
        # An import and an administrator must both acquire Agent before Prompt.
        # The hook signals when the import has reached history recording: before
        # the fix it holds Prompt and blocks on the administrator's Agent lock.
        reached_history = asyncio.Event()
        original_record_prompt = dao._record_for_prompt
        async def record_prompt(*args, **kwargs):
            reached_history.set()
            return await original_record_prompt(*args, **kwargs)
        monkeypatch.setattr(dao, "_record_for_prompt", record_prompt)
        async with factory() as admin_session, factory() as import_session:
            await dao._lock_agent(admin_session, AGENT)
            async def import_prompt():
                try:
                    await dao.upsert_prompt_with_id(import_session, PROMPT,
                        'Fixture prompt', 'imported prompt', None, 'admin', commit=False)
                    await import_session.commit()
                except BaseException:
                    await import_session.rollback()
                    raise
            pending = asyncio.create_task(import_prompt())
            try:
                await asyncio.wait_for(reached_history.wait(), .2)
            except asyncio.TimeoutError:
                pass  # Correct order: import waits for Agent, not while holding Prompt.
            async def record_admin():
                try:
                    await dao.record_version(admin_session, AGENT, 'admin', commit=False)
                    await admin_session.commit()
                except BaseException:
                    await admin_session.rollback()
                    raise
            outcomes = await asyncio.wait_for(asyncio.gather(record_admin(), pending, return_exceptions=True), 5)
            assert not any(isinstance(value, BaseException) for value in outcomes), outcomes
            check('import-and-admin-use-consistent-lock-order', True)
        # Two supported Agents can share one Prompt. Both administrator entry
        # points must acquire the same complete Agent set in the same order.
        other_agent = 'studio_smart_qa'
        async with factory() as s:
            s.add(AgentRow(f_agent_id=other_agent, f_name='Second fixture',
                f_mode='chat', f_prompt_id=PROMPT, f_status='published',
                f_create_user='seed', f_update_user='seed', f_create_time=1, f_update_time=1))
            await s.commit()
            await dao.record_version(s, other_agent, 'seed')
        async def publish_shared(agent_id, content):
            async with factory() as s:
                return await routes.publish_shared_prompt(PromptPublish(content=content),
                    agent_id, Account('admin', 'user'), s)
        await asyncio.wait_for(asyncio.gather(
            publish_shared(AGENT, 'shared-first'),
            publish_shared(other_agent, 'shared-second')), 5)
        async with factory() as s:
            prompt = await dao.get_prompt(s, PROMPT)
            for agent_id in (AGENT, other_agent):
                versions = await dao.list_versions(s, agent_id)
                current = [v for v in versions if v.f_current]
                assert len(current) == 1 and current[0].f_prompt_version == prompt.current_version
            check('shared-prompt-admins-lock-all-agents-in-order', True)
        # EXPLAIN must prove the live MariaDB path uses the binding index.
        async with factory() as s:
            plan = (await s.execute(text(
                "EXPLAIN SELECT * FROM t_agent FORCE INDEX (ix_agent_prompt_id_agent_id) "
                "WHERE f_prompt_id=:prompt ORDER BY f_agent_id FOR UPDATE"
            ), {"prompt": PROMPT})).mappings().one()
            check('binding-current-read-uses-index', plan['key'] == 'ix_agent_prompt_id_agent_id' and plan['type'] == 'ref')
            print(json.dumps({'binding_lock_explain': dict(plan)}))
            s.add(AgentRow(f_agent_id='unrelated', f_name='Unrelated fixture',
                f_mode='chat', f_prompt_id=None, f_status='published',
                f_create_user='seed', f_update_user='seed', f_create_time=1, f_update_time=1))
            await s.commit()
        async with factory() as locked, factory() as writer:
            await dao.lock_prompt_agents(locked, PROMPT)
            await asyncio.wait_for(dao.update_shared_agent(
                writer, 'unrelated', {'model': 'independent'}, 'admin'), 3)
            check('unrelated-agent-write-not-blocked', True)
            await locked.rollback()
        async with factory() as stale_session, factory() as rebinder:
            # A current locking read must include bindings newer than this RR snapshot.
            await stale_session.get(AgentRow, 'unrelated')
            rebound = await rebinder.get(AgentRow, 'unrelated')
            rebound.f_prompt_id = PROMPT
            await rebinder.commit()
            await dao.record_version(rebinder, 'unrelated', 'admin')
            ids = await dao.lock_prompt_agents(stale_session, PROMPT)
            check('binding-newer-than-rr-snapshot-locked', 'unrelated' in ids)
            published = await dao.publish_prompt_version(stale_session, PROMPT,
                'after-rebinding', None, 'admin', commit=False)
            await stale_session.commit()
        async with factory() as s:
            current = await dao.current_version(s, 'unrelated')
            check('newly-bound-agent-gets-published-history', current.f_prompt_version == published.current_version)
        async with factory() as locked, factory() as binder:
            await dao.lock_prompt_agents(locked, PROMPT)
            async def new_binding():
                binder.add(AgentRow(f_agent_id='new-binding', f_name='New binding fixture',
                    f_mode='chat', f_prompt_id=PROMPT, f_status='published',
                    f_create_user='seed', f_update_user='seed', f_create_time=1, f_update_time=1))
                await binder.flush()
                await dao.record_version(binder, 'new-binding', 'admin', commit=False)
                await binder.commit()
            pending = asyncio.create_task(new_binding())
            await asyncio.sleep(.15)
            check('new-binding-waits-for-current-range', not pending.done())
            published = await dao.publish_prompt_version(locked, PROMPT,
                'while-binding-waits', None, 'admin', commit=False)
            await locked.commit()
            await asyncio.wait_for(pending, 3)
        async with factory() as s:
            current = await dao.current_version(s, 'new-binding')
            check('new-binding-resumes-with-current-prompt-history', current.f_prompt_version == published.current_version)
        # Rebinding an existing Agent out of the locked range is serialized as
        # well; when it resumes its version must describe the new binding.
        async with factory() as s:
            s.add_all([PromptRow(f_prompt_id='rebind-target', f_name='Rebind target',
                f_current_version=1, f_update_user='seed', f_update_time=1),
                PromptVersionRow(f_prompt_id='rebind-target', f_version=1,
                    f_content='target canonical', f_create_user='seed', f_create_time=1)])
            await s.commit()
        async with factory() as locked, factory() as rebinder:
            await dao.lock_prompt_agents(locked, PROMPT)
            async def change_binding():
                row = await rebinder.get(AgentRow, 'unrelated')
                row.f_prompt_id = 'rebind-target'
                await rebinder.flush()
                await dao.record_version(rebinder, 'unrelated', 'admin', commit=False)
                await rebinder.commit()
            pending = asyncio.create_task(change_binding())
            await asyncio.sleep(.15)
            check('rebind-waits-for-current-binding-range', not pending.done())
            await dao.publish_prompt_version(locked, PROMPT,
                'before-rebind-completes', None, 'admin', commit=False)
            await locked.commit()
            await asyncio.wait_for(pending, 3)
        async with factory() as s:
            current = await dao.current_version(s, 'unrelated')
            row = await s.get(AgentRow, 'unrelated')
            check('rebind-records-target-prompt-history', row.f_prompt_id == 'rebind-target' and current.f_prompt_version == 1)
            # Both null and a different nonnull Prompt binding remain writable.
            s.add(AgentRow(f_agent_id='null-unrelated', f_name='Null unrelated fixture',
                f_mode='chat', f_prompt_id=None, f_status='published',
                f_create_user='seed', f_update_user='seed', f_create_time=1, f_update_time=1))
            await s.commit()
        async with factory() as locked:
            await dao.lock_prompt_agents(locked, PROMPT)
            for agent_id in ('unrelated', 'null-unrelated'):
                async with factory() as writer:
                    await asyncio.wait_for(dao.update_shared_agent(
                        writer, agent_id, {'model': 'independent-again'}, 'admin'), 3)
            check('other-prompt-and-null-bound-agents-remain-writable', True)
            await locked.rollback()
        # An administrator initially reads an old binding while an internal edit
        # rebinds it. The old range is released before locking the new one.
        other_prompt = 'other-fixture'
        async with factory() as s:
            s.add_all([PromptRow(f_prompt_id=other_prompt, f_name='Other fixture prompt',
                f_current_version=1, f_update_user='seed', f_update_time=1),
                PromptVersionRow(f_prompt_id=other_prompt, f_version=1,
                    f_content='other canonical', f_create_user='seed', f_create_time=1)])
            await s.commit()
        original_lock = dao.lock_prompt_agents
        rebound_once = False
        async def rebind_before_lock(session, prompt_id):
            nonlocal rebound_once
            if not rebound_once:
                rebound_once = True
                async with factory() as writer:
                    row = await writer.get(AgentRow, AGENT)
                    row.f_prompt_id = other_prompt
                    await writer.commit()
            return await original_lock(session, prompt_id)
        monkeypatch.setattr(dao, 'lock_prompt_agents', rebind_before_lock)
        try:
            async with factory() as s:
                result = await routes.publish_shared_prompt(PromptPublish(content='new binding admin'),
                    AGENT, Account('admin', 'user'), s)
                check('admin-restarts-before-locking-rebound-prompt', result.prompt.prompt_id == other_prompt and result.prompt.content == 'new binding admin')
        finally:
            monkeypatch.setattr(dao, 'lock_prompt_agents', original_lock)
        # ORM identity-map entries are weak references. Even with the binding
        # unchanged, the editor must return current values after lock acquisition,
        # not perform a plain read against its initial repeatable-read snapshot.
        updated_once = False
        latest_tools = [{'type': 'context_loader', 'profile': 'latest-profile'}]
        async def update_before_lock(session, prompt_id):
            nonlocal updated_once
            if not updated_once:
                updated_once = True
                async with factory() as writer:
                    await dao.update_shared_agent(writer, AGENT,
                        {'model': 'latest-model', 'tools': latest_tools}, 'other-admin')
            return await original_lock(session, prompt_id)
        monkeypatch.setattr(dao, 'lock_prompt_agents', update_before_lock)
        try:
            async with factory() as s:
                editor_agent = await routes._shared_editor(s, Account('admin', 'user'), AGENT)
                check('editor-returns-current-model-and-tools-after-lock',
                    editor_agent.model == 'latest-model' and editor_agent.tools == latest_tools)
                await s.rollback()
        finally:
            monkeypatch.setattr(dao, 'lock_prompt_agents', original_lock)
        # A body-only publisher can have an older repeatable-read snapshot
        # while waiting for another publisher to replace the active schema.
        schema = {'type': 'object', 'properties': {'question': {'type': 'string'}}}
        async with factory() as writer, factory() as follower:
            before = await dao.get_prompt(follower, other_prompt)
            assert before.vars_schema is None
            await dao.publish_prompt_version(writer, other_prompt,
                'schema writer', schema, 'admin', commit=False)
            pending = asyncio.create_task(routes.publish_shared_prompt(
                PromptPublish(content='body-only follower'), AGENT, Account('admin', 'user'), follower))
            await asyncio.sleep(0.15)
            check('body-only-publisher-waits-for-schema-writer', not pending.done())
            await writer.commit()
            result = await asyncio.wait_for(pending, 3)
            check('body-only-publisher-inherits-current-schema-after-wait',
                result.prompt.vars_schema == schema and result.prompt.content == 'body-only follower')
        from fastapi import HTTPException

        from app.models import PromptVersionPublish

        async with factory() as s:
            target = await dao.next_prompt_version(s, other_prompt)
            prompt_count = len(await dao.list_prompt_versions(s, other_prompt))
            agent_count = len(await dao.list_versions(s, AGENT))

        async def publish_target(content):
            async with factory() as s:
                try:
                    return await routes.publish_shared_prompt_version(
                        PromptVersionPublish(content=content, version=target),
                        agent_id=AGENT, account=Account('admin', 'user'), session=s)
                except HTTPException as failure:
                    return failure

        outcomes = await asyncio.wait_for(asyncio.gather(
            publish_target('same-version admin A'), publish_target('same-version admin B')), 5)
        check('same-target-publishers-one-success-one-conflict',
            sorted(value.status_code if isinstance(value, HTTPException) else 200 for value in outcomes) == [200, 409])
        replay = await publish_target('same-version retry')
        check('committed-target-retry-is-conflict', isinstance(replay, HTTPException) and replay.status_code == 409)
        async with factory() as s:
            check('target-conflicts-create-no-prompt-or-agent-history',
                len(await dao.list_prompt_versions(s, other_prompt)) == prompt_count + 1 and
                len(await dao.list_versions(s, AGENT)) == agent_count + 1)
    finally:
        await engine.dispose()
        with db.cursor() as c:
            c.execute(f'DROP DATABASE IF EXISTS `{name}`')
        db.close()
    print(json.dumps({'passed': len(checks), 'total': len(checks), 'real_mariadb': True, 'database_dropped': True}))

@pytest.mark.skipif(not os.getenv('BKN_AGENT_TEST_MARIADB_HOST'), reason='isolated MariaDB not configured')
def test_admin_transactions_real_mariadb(monkeypatch):
    asyncio.run(main(monkeypatch))
