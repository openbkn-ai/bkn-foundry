from fastapi.testclient import TestClient

from app.db import get_session
from app.main import app


def test_internal_authorization_resources_serves_agent_and_template_names(monkeypatch):
    from app import dao

    async def fake_session():
        yield None

    observed = []

    async def fake_list(session, name, direction, offset, limit):
        observed.append((name, direction, offset, limit))
        return [("agent-1", "ReadableAgent")], 1

    app.dependency_overrides[get_session] = fake_session
    monkeypatch.setattr(dao, "list_authorization_resources", fake_list)
    client = TestClient(app)
    try:
        for resource_type in ("agent", "agent_tpl"):
            response = client.get(
                "/api/bkn-agent/in/v1/authorization-resources",
                params={
                    "resource_type": resource_type,
                    "name": " Readable ",
                    "direction": "desc",
                    "offset": 2,
                    "limit": 10,
                },
            )
            assert response.status_code == 200
            assert response.json() == {
                "entries": [{"id": "agent-1", "name": "ReadableAgent"}],
                "total": 1,
            }
    finally:
        app.dependency_overrides.pop(get_session, None)

    assert observed == [("Readable", "desc", 2, 10), ("Readable", "desc", 2, 10)]


def test_internal_authorization_resources_rejects_unknown_type():
    response = TestClient(app).get(
        "/api/bkn-agent/in/v1/authorization-resources",
        params={"resource_type": "unknown"},
    )
    assert response.status_code == 400
