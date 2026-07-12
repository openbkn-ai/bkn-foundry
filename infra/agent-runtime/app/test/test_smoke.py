from fastapi.testclient import TestClient

from app.main import app

client = TestClient(app)
SVC = {"x-account-id": "svc-test", "x-account-type": "app"}


def test_health():
    r = client.get("/api/v1/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_auth_fail_closed_without_identity():
    r = client.get("/api/agent-runtime/v1/agents")
    assert r.status_code == 401
    assert r.json()["detail"]["code"] == "AgentRuntime.Auth.AccountRequired"


def test_auth_rejects_anonymous():
    r = client.get(
        "/api/agent-runtime/v1/agents",
        headers={"x-account-id": "x", "x-account-type": "anonymous"},
    )
    assert r.status_code == 401


def test_validation_error_uses_platform_error_shape():
    r = client.post("/api/agent-runtime/v1/chat", json={"agent_id": "a"}, headers=SVC)
    assert r.status_code == 400
    body = r.json()
    assert body["code"] == "AgentRuntime.ParamError.FormatError"
    assert body["solution"]
