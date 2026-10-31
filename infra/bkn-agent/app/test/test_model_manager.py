# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Caller-authorized model resolution is shared by Agent features."""

import asyncio

import aiohttp
import pytest
from fastapi import HTTPException

from app import auth, model_manager


class Response:
    def __init__(self, status, payload=None):
        self.status = status
        self.payload = payload

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_args):
        return None

    async def json(self):
        if isinstance(self.payload, Exception):
            raise self.payload
        return self.payload


class Client:
    def __init__(self, response, seen, *, timeout):
        self.response = response
        self.seen = seen
        self.seen["timeout"] = timeout.total

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_args):
        return None

    def get(self, url, **kwargs):
        self.seen["request"] = (url, kwargs)
        if isinstance(self.response, Exception):
            raise self.response
        return self.response


def resolve(monkeypatch, response, *, token="Bearer caller-token"):
    seen = {}
    monkeypatch.setattr(
        model_manager.aiohttp,
        "ClientSession",
        lambda *, timeout: Client(response, seen, timeout=timeout),
    )
    context = auth.set_caller_token(token)
    try:
        result = asyncio.run(model_manager.resolve_authorized_model("model-1"))
    finally:
        auth.reset_caller_token(context)
    return result, seen


def test_resolves_model_metadata_with_the_callers_authorization(monkeypatch):
    result, seen = resolve(
        monkeypatch,
        Response(
            200,
            {
                "model_id": "model-1",
                "model_type": "rlm",
                "model_name": "provider-model-name",
            },
        ),
    )

    assert result == model_manager.ResolvedModel(
        model_id="model-1",
        model_type="rlm",
        model_name="provider-model-name",
    )
    url, request = seen["request"]
    assert url == f"{model_manager.config.MF_MODEL_MANAGER_PUBLIC_BASE}/llm/get"
    assert request == {
        "params": {"model_id": "model-1"},
        "headers": {"Authorization": "Bearer caller-token"},
        "allow_redirects": False,
    }
    assert seen["timeout"] == 5


def test_requires_the_callers_authorization(monkeypatch):
    with pytest.raises(HTTPException) as raised:
        resolve(monkeypatch, Response(200, {}), token=None)

    assert raised.value.status_code == 401


@pytest.mark.parametrize(
    ("upstream_status", "expected_status"),
    [(400, 400), (404, 400), (401, 401), (403, 403), (429, 503), (500, 503)],
)
def test_maps_model_manager_statuses(monkeypatch, upstream_status, expected_status):
    with pytest.raises(HTTPException) as raised:
        resolve(monkeypatch, Response(upstream_status))

    assert raised.value.status_code == expected_status


@pytest.mark.parametrize(
    "payload",
    [
        None,
        [],
        {"model_id": "another", "model_type": "llm", "model_name": "name"},
        {"model_id": "model-1", "model_type": "", "model_name": "name"},
        {"model_id": "model-1", "model_type": " llm", "model_name": "name"},
        {"model_id": "model-1", "model_type": "llm", "model_name": ""},
        {"model_id": "model-1", "model_type": "llm", "model_name": " name"},
    ],
)
def test_rejects_untrusted_model_metadata(monkeypatch, payload):
    with pytest.raises(HTTPException) as raised:
        resolve(monkeypatch, Response(200, payload))

    assert raised.value.status_code == 400


@pytest.mark.parametrize(
    "failure",
    [
        aiohttp.ClientConnectionError("private details"),
        TimeoutError("private details"),
        ValueError("private details"),
    ],
)
def test_hides_model_manager_transport_and_payload_failures(monkeypatch, failure):
    response = Response(200, failure) if isinstance(failure, ValueError) else failure
    with pytest.raises(HTTPException) as raised:
        resolve(monkeypatch, response)

    assert raised.value.status_code == 503
