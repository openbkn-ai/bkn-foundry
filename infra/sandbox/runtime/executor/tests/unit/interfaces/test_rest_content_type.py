"""Regression tests for request Content-Type handling in the executor app."""

from unittest.mock import patch

from fastapi import APIRouter
from fastapi.testclient import TestClient

from executor.interfaces.http.rest import create_app


def _create_app_without_side_effects():
    with (
        patch("executor.interfaces.http.rest.BubblewrapRunner"),
        patch("executor.interfaces.http.rest.CallbackClient"),
        patch("executor.interfaces.http.rest.ArtifactScanner"),
        patch("executor.interfaces.http.rest.HeartbeatService"),
        patch("executor.interfaces.http.rest.LifecycleService"),
        patch("executor.interfaces.http.rest.ExecuteCodeCommand"),
        patch("executor.interfaces.http.rest.SessionConfigSyncService"),
    ):
        return create_app()


def test_json_body_without_content_type_is_accepted():
    """FastAPI >= 0.132 rejects such bodies by default; the executor keeps accepting them."""
    app = _create_app_without_side_effects()

    probe = APIRouter()

    @probe.post("/content-type-probe")
    async def content_type_probe(payload: dict) -> dict:
        return payload

    app.include_router(probe)

    client = TestClient(app)
    response = client.post("/content-type-probe", content=b'{"execution_id": "exec_001"}')

    assert "content-type" not in response.request.headers
    assert response.status_code == 200
    assert response.json() == {"execution_id": "exec_001"}
