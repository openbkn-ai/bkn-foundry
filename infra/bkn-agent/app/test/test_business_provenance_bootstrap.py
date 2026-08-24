"""Enterprise business-provenance Agent bootstrap validation."""

import json
import asyncio
from pathlib import Path

import pytest
from fastapi import HTTPException
from fastapi.testclient import TestClient
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine
from starlette.requests import Request

from app.bootstrap.business_provenance import BootstrapError, bootstrap
from app.auth import get_account
from app.config import config
from app.db import get_session
from app.main import app
from app.models import AgentRow, Base, PromptRow


PACKAGE = (
    Path(__file__).resolve().parents[2]
    / "deploy"
    / "agents"
    / "business-provenance-optimizer.json"
)
OWNER_ID = "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb"
BOOTSTRAP_TOKEN = "deployment-bootstrap-secret"


class FakeClient:
    def __init__(self, owner=None, import_result=None, agent=None):
        self.owner = owner or {
            "id": OWNER_ID,
            "account": "openbkn-business-provenance",
            "name": "OpenBKN Business Provenance Service",
            "enabled": True,
            "account_type": "app",
        }
        package_item = json.loads(PACKAGE.read_text(encoding="utf-8"))["items"][0]
        self.import_result = import_result or {
            "results": [
                {
                    "agent_id": package_item["agent_id"],
                    "action": "created",
                    "prompt_action": "created",
                }
            ],
            "warnings": [],
        }
        self.agent = agent or {
            **package_item["spec"],
            "agent_id": package_item["agent_id"],
            "create_user": OWNER_ID,
        }
        self.import_headers = None
        self.agent_get_headers = None

    def get_json(self, url, headers=None):
        if "/directory/users/" in url:
            return self.owner
        self.agent_get_headers = headers
        return self.agent

    def post_json(self, url, body, headers):
        self.import_headers = headers
        return self.import_result


def test_bootstrap_imports_and_verifies_the_canonical_agent():
    client = FakeClient()

    result = bootstrap(
        client,
        package_path=PACKAGE,
        bkn_safe_url="http://bkn-safe:3000/api/safe/v1",
        bkn_agent_url="http://bkn-agent:30800/api/bkn-agent/v1",
        owner_id=OWNER_ID,
        bootstrap_token=BOOTSTRAP_TOKEN,
    )

    assert result == "created"
    assert client.import_headers == {
        "x-account-id": OWNER_ID,
        "x-account-type": "app",
        "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN,
    }
    assert client.agent_get_headers == client.import_headers


def test_repeated_upgrade_updates_agent_without_new_prompt_version():
    client = FakeClient()
    client.import_result["results"][0].update(
        action="updated",
        prompt_action="unchanged",
    )

    result = bootstrap(
        client, PACKAGE, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
    )

    assert result == "updated"


def test_bootstrap_does_not_require_a_package_specific_default_model():
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    assert package["items"][0]["spec"]["model"] == ""

    assert (
        bootstrap(
            FakeClient(), PACKAGE, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
        )
        == "created"
    )


@pytest.mark.parametrize(
    "owner_change",
    [
        {"enabled": False},
        {"account_type": "other"},
        {"account": "wrong-account"},
    ],
)
def test_bootstrap_rejects_missing_or_conflicting_owner(owner_change):
    client = FakeClient()
    client.owner.update(owner_change)

    with pytest.raises(BootstrapError, match="owner"):
        bootstrap(
            client, PACKAGE, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
        )


def test_bootstrap_rejects_per_item_import_failure_even_when_http_succeeded():
    client = FakeClient(
        import_result={
            "results": [
                {
                    "agent_id": "business_provenance_optimizer",
                    "action": "failed",
                    "prompt_action": "none",
                    "error": "owned by another account",
                }
            ],
            "warnings": [],
        }
    )

    with pytest.raises(BootstrapError, match="owned by another account"):
        bootstrap(
            client, PACKAGE, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
        )


def test_bootstrap_rejects_agent_spec_drift_after_import():
    client = FakeClient()
    client.agent["status"] = "draft"

    with pytest.raises(BootstrapError, match="status"):
        bootstrap(
            client, PACKAGE, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
        )


def _request(headers: dict[str, str]) -> Request:
    return Request(
        {
            "type": "http",
            "method": "POST",
            "path": "/api/bkn-agent/v1/import",
            "headers": [(key.lower().encode(), value.encode()) for key, value in headers.items()],
        }
    )


def test_fixed_owner_identity_requires_the_deployment_secret(monkeypatch):
    monkeypatch.setattr(config, "PROVENANCE_BOOTSTRAP_TOKEN", BOOTSTRAP_TOKEN)
    identity = {"x-account-id": OWNER_ID, "x-account-type": "app"}

    for headers in (
        identity,
        {**identity, "x-bkn-provenance-bootstrap-token": "forged"},
        {
            **identity,
            "x-account-type": "user",
            "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN,
        },
    ):
        with pytest.raises(HTTPException) as exc:
            get_account(_request(headers))
        assert exc.value.status_code == 401

    account = get_account(
        _request({**identity, "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN})
    )
    assert account.account_id == OWNER_ID
    assert account.account_type == "app"


def test_canonical_package_import_is_idempotent_with_real_database(tmp_path, monkeypatch):
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
    monkeypatch.setattr(config, "PROVENANCE_BOOTSTRAP_TOKEN", BOOTSTRAP_TOKEN)
    package = json.loads(PACKAGE.read_text(encoding="utf-8"))
    headers = {
        "x-account-id": OWNER_ID,
        "x-account-type": "app",
        "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN,
    }
    client = TestClient(app)
    try:
        fresh = client.post("/api/bkn-agent/v1/import", json={"package": package}, headers=headers)
        repeat = client.post("/api/bkn-agent/v1/import", json={"package": package}, headers=headers)
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
                prompt = await session.get(PromptRow, "business_provenance_optimizer_prompt")
                return agent, prompt

        agent, prompt = asyncio.run(verify())
        assert agent.f_create_user == OWNER_ID
        assert agent.f_model == ""
        assert agent.f_status == "published"
        assert agent.f_tools == package["items"][0]["spec"]["tools"]
        assert prompt.f_current_version == 1
    finally:
        app.dependency_overrides.pop(get_session, None)
        asyncio.run(engine.dispose())
