"""Business-provenance defaults and retired-identity regression coverage."""

import asyncio

import pytest
from fastapi import HTTPException
from fastapi.testclient import TestClient
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine
from starlette.requests import Request

from app.auth import get_account
from app.db import get_session
from app.main import app
from app.models import AgentRow, Base, PromptRow
from app.test.test_shared_agent_seeds import seed_package

OWNER_ID = "internal-import-test-owner"


@pytest.mark.parametrize("account_type", ["app", "user"])
@pytest.mark.parametrize("token", [None, "old-deployment-secret"])
def test_retired_identity_is_rejected(account_type, token):
    headers = {
        "x-account-id": "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb",
        "x-account-type": account_type,
    }
    if token:
        headers["x-bkn-provenance-bootstrap-token"] = token
    request = Request({
        "type": "http",
        "headers": [(k.encode(), v.encode()) for k, v in headers.items()],
    })
    with pytest.raises(HTTPException) as exc:
        get_account(request)
    assert exc.value.status_code == 401


@pytest.mark.parametrize("account_type", ["app", "user"])
def test_other_internal_callers_keep_their_identity(account_type):
    request = Request({
        "type": "http",
        "headers": [
            (b"x-account-id", b"caller"),
            (b"x-account-type", account_type.encode()),
        ],
    })
    account = get_account(request)
    assert account.account_id == "caller"
    assert account.account_type == account_type


def test_manual_import_retains_existing_upsert_semantics(tmp_path):
    database = tmp_path / "bkn-agent.db"
    engine = create_async_engine(f"sqlite+aiosqlite:///{database}")
    sessions = async_sessionmaker(engine, expire_on_commit=False)

    async def prepare():
        async with engine.begin() as connection:
            await connection.run_sync(Base.metadata.create_all)

    async def session_override():
        async with sessions() as session:
            yield session

    asyncio.run(prepare())
    app.dependency_overrides[get_session] = session_override
    package = seed_package()
    headers = {
        "x-account-id": OWNER_ID,
        "x-account-type": "app",
    }
    client = TestClient(app)
    try:
        fresh = client.post(
            "/api/bkn-agent/v1/import", json={"package": package}, headers=headers
        )
        assert fresh.status_code == 200, fresh.text
        assert fresh.json()["results"][0]["action"] == "created", fresh.text
        customized_spec = {**package["items"][0]["spec"], "model": "custom-model"}
        customized_spec["limits"] = {**customized_spec["limits"], "timeout_s": 180}
        customized = client.put(
            "/api/bkn-agent/v1/agents/business_provenance_optimizer",
            json=customized_spec,
            headers=headers,
        )
        assert customized.status_code == 200
        assert customized.json()["model"] == "custom-model"
        assert customized.json()["limits"]["timeout_s"] == 180
        # Existing upsert semantics overwrite manual settings, not just timeout.
        repeat = client.post(
            "/api/bkn-agent/v1/import", json={"package": package}, headers=headers
        )
        foreign = client.post(
            "/api/bkn-agent/v1/import",
            json={"package": package},
            headers={"x-account-id": "foreign-owner", "x-account-type": "app"},
        )

        assert fresh.status_code == repeat.status_code == foreign.status_code == 200
        assert fresh.json()["results"][0]["prompt_action"] == "created"
        repeat_item = repeat.json()["results"][0]
        assert repeat_item["action"] == "updated"
        assert repeat_item["prompt_action"] == "unchanged"
        assert foreign.json()["results"][0]["action"] == "failed"

        async def verify():
            async with sessions() as session:
                agent = await session.get(AgentRow, "business_provenance_optimizer")
                prompt = await session.get(
                    PromptRow, "business_provenance_optimizer_prompt"
                )
                return agent, prompt

        agent, prompt = asyncio.run(verify())
        assert agent.f_create_user == OWNER_ID
        assert agent.f_model == ""
        assert agent.f_limits == package["items"][0]["spec"]["limits"]
        assert agent.f_limits["timeout_s"] == 600
        assert agent.f_status == "published"
        assert agent.f_tools == package["items"][0]["spec"]["tools"]
        assert prompt.f_current_version == 1
    finally:
        app.dependency_overrides.pop(get_session, None)
        asyncio.run(engine.dispose())


def test_claim_attribution_sql_default_is_published_without_tools():
    item = seed_package("business_provenance_claim_attribution")["items"][0]
    assert item["spec"]["status"] == "published"
    assert item["spec"]["mode"] == "chat"
    assert item["spec"]["tools"] == []
    assert item["spec"]["limits"]["max_tool_calls"] == 0
    assert "不得把孤立数字" in item["prompt"]["content"]
    assert "Mario Kempes" in item["prompt"]["content"]


