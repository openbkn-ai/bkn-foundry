"""EE compatibility entry verifies SQL defaults without resetting shared configuration."""

import asyncio
from io import BytesIO
from urllib.error import HTTPError

import pytest
from fastapi import HTTPException
from fastapi.testclient import TestClient
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine
from starlette.requests import Request

from app.auth import get_account
from app.bootstrap import business_provenance
from app.bootstrap.business_provenance import BootstrapError, JsonClient, bootstrap
from app.config import config
from app.db import get_session
from app.main import app
from app.models import AgentRow, Base, PromptRow
from app.test.test_shared_agent_seeds import seed_package

OWNER_ID = "bdd59f76-19c3-58b0-bf5f-082c4c3cbddb"
BOOTSTRAP_TOKEN = "deployment-bootstrap-secret"
AGENT_ID = "business_provenance_optimizer"


class FakeClient:
    def __init__(self):
        self.owner = {
            "id": OWNER_ID,
            "account": "openbkn-business-provenance",
            "name": "OpenBKN Business Provenance Service",
            "enabled": True,
            "account_type": "app",
        }
        item = seed_package()["items"][0]
        self.agent = {**item["spec"], "create_user": OWNER_ID}
        self.prompt = item["prompt"]
        self.headers = []

    def get_json(self, url, headers=None, retry_statuses=None):
        if "/directory/users/" in url:
            return self.owner
        self.headers.append(headers)
        return self.prompt if "/prompts/" in url else self.agent

    def post_json(self, *args, **kwargs):
        raise AssertionError("bootstrap must not write shared configuration")


def verify(client):
    return bootstrap(
        client, AGENT_ID, "http://safe", "http://agent", OWNER_ID, BOOTSTRAP_TOKEN
    )


def test_compatibility_entry_only_reads_sql_seeded_configuration():
    client = FakeClient()
    assert verify(client) == "verified"
    assert (
        client.headers
        == [
            {
                "x-account-id": OWNER_ID,
                "x-account-type": "app",
                "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN,
            }
        ]
        * 2
    )


def test_verification_accepts_administrator_edits_and_disabled_state():
    client = FakeClient()
    client.agent.update(model="custom", status="draft")
    client.prompt["content"] = "shared customer configuration"
    assert verify(client) == verify(client) == "verified"
    assert client.agent["model"] == "custom"
    assert client.prompt["content"] == "shared customer configuration"


@pytest.mark.parametrize(
    "change", [{"enabled": False}, {"account_type": "other"}, {"account": "wrong"}]
)
def test_bootstrap_rejects_conflicting_owner(change):
    client = FakeClient()
    client.owner.update(change)
    with pytest.raises(BootstrapError, match="owner"):
        verify(client)


@pytest.mark.parametrize(
    "change", [{"agent_id": "foreign"}, {"create_user": "foreign"}]
)
def test_verification_rejects_foreign_agent_or_owner(change):
    client = FakeClient()
    client.agent.update(change)
    with pytest.raises(BootstrapError, match="identity or owner"):
        verify(client)


def test_verification_rejects_missing_shared_prompt():
    client = FakeClient()
    client.prompt["content"] = ""
    with pytest.raises(BootstrapError, match="Prompt"):
        verify(client)


def test_json_client_retries_owner_not_found_during_rolling_upgrade(monkeypatch):
    calls = 0

    def urlopen(request, timeout):
        nonlocal calls
        calls += 1
        if calls == 1:
            raise HTTPError(
                request.full_url, 404, "not found", {}, BytesIO(b"not ready")
            )
        return BytesIO(b'{"id":"ready"}')

    monkeypatch.setattr(business_provenance, "urlopen", urlopen)
    assert JsonClient(attempts=2, delay_s=0).get_json(
        "http://safe/directory/users/owner", retry_statuses={404}
    ) == {"id": "ready"}
    assert calls == 2


def _request(headers: dict[str, str]) -> Request:
    return Request(
        {
            "type": "http",
            "method": "POST",
            "path": "/api/bkn-agent/v1/import",
            "headers": [
                (key.lower().encode(), value.encode()) for key, value in headers.items()
            ],
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


def test_canonical_package_import_is_idempotent_with_real_database(
    tmp_path, monkeypatch
):
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
    package = seed_package()
    headers = {
        "x-account-id": OWNER_ID,
        "x-account-type": "app",
        "x-bkn-provenance-bootstrap-token": BOOTSTRAP_TOKEN,
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


def test_main_keeps_the_legacy_deployment_command_and_only_verifies_both_agents(
    monkeypatch,
):
    calls = []
    for name in ("BKN_PROVENANCE_PACKAGE", "BKN_CLAIM_ATTRIBUTION_PACKAGE"):
        monkeypatch.delenv(name, raising=False)
    for name, value in {
        "BKN_SAFE_URL": "http://safe",
        "BKN_AGENT_URL": "http://agent",
        "BKN_PROVENANCE_OWNER_ID": OWNER_ID,
        "BKN_PROVENANCE_BOOTSTRAP_TOKEN": BOOTSTRAP_TOKEN,
    }.items():
        monkeypatch.setenv(name, value)

    def verify_only(client, agent_id, *args):
        calls.append(agent_id)
        return "verified"

    monkeypatch.setattr(business_provenance, "bootstrap", verify_only)
    assert business_provenance.main() == 0
    assert calls == [AGENT_ID, "business_provenance_claim_attribution"]


def test_main_does_not_silently_ignore_old_json_overrides(monkeypatch, capsys):
    monkeypatch.setenv("BKN_PROVENANCE_PACKAGE", "/custom/package.json")
    assert business_provenance.main() == 1
    assert "no longer supported" in capsys.readouterr().err
