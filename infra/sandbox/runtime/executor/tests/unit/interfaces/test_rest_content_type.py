"""Request Content-Type handling in the executor app.

The executor keeps FastAPI's strict default (>= 0.132): its only caller, the
control plane's ExecutorClient, always sends ``Content-Type: application/json``.
"""

from contextlib import contextmanager
from unittest.mock import patch

from fastapi import APIRouter
from fastapi.testclient import TestClient

from executor.interfaces.http.rest import create_app


@contextmanager
def _client_without_side_effects():
    # Keep the patches active for the client's whole lifetime: the patched
    # classes are built in lifespan, not in create_app().
    with (
        patch("executor.interfaces.http.rest.BubblewrapRunner"),
        patch("executor.interfaces.http.rest.CallbackClient"),
        patch("executor.interfaces.http.rest.ArtifactScanner"),
        patch("executor.interfaces.http.rest.HeartbeatService"),
        patch("executor.interfaces.http.rest.LifecycleService"),
        patch("executor.interfaces.http.rest.ExecuteCodeCommand"),
        patch("executor.interfaces.http.rest.SessionConfigSyncService"),
    ):
        app = create_app()
        probe = APIRouter()

        @probe.post("/content-type-probe")
        async def content_type_probe(payload: dict) -> dict:
            return payload

        app.include_router(probe)
        yield TestClient(app)


def test_json_body_without_content_type_is_rejected():
    with _client_without_side_effects() as client:
        response = client.post("/content-type-probe", content=b'{"execution_id": "exec_001"}')

    assert "content-type" not in response.request.headers
    assert response.status_code == 422


def test_json_body_with_content_type_is_accepted():
    with _client_without_side_effects() as client:
        response = client.post("/content-type-probe", json={"execution_id": "exec_001"})

    assert response.status_code == 200
    assert response.json() == {"execution_id": "exec_001"}
