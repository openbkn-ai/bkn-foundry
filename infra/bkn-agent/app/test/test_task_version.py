# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Read-only configuration pins use the same caller boundary as Agent reads."""

import asyncio
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from fastapi import HTTPException

from app import dao
from app.auth import Account
from app.routers.agents import get_task_version
from app.test.test_agent_settings import agent


def test_task_version_receipt_exposes_no_prompt_or_configuration_body(monkeypatch):
    current = agent(mode="task", tools=[])
    monkeypatch.setattr(dao, "get_agent", AsyncMock(return_value=current))
    monkeypatch.setattr(
        dao, "current_version",
        AsyncMock(return_value=SimpleNamespace(
            f_version=7, f_prompt_version=3, f_deleted=False, f_tools=[],
        )),
    )
    session = object()  # No commit/write methods are available on this read path.
    result = asyncio.run(get_task_version("studio_smart_qa", Account("owner", "user"), session))
    assert result.model_dump() == {
        "agent_id": "studio_smart_qa", "agent_version": 7,
        "prompt_version": 3, "tool_free": True,
    }
    dao.get_agent.assert_awaited_once_with(session, "studio_smart_qa")
    dao.current_version.assert_awaited_once_with(session, "studio_smart_qa")


@pytest.mark.parametrize("scenario,status", [
    ("missing", 404), ("chat", 400), ("draft", 400),
    ("no_snapshot", 409), ("deleted", 409),
])
def test_task_version_refuses_unrunnable_or_unpinned_configuration(monkeypatch, scenario, status):
    current = agent(mode="task", tools=[])
    if scenario == "chat":
        current.mode = "chat"
    elif scenario == "draft":
        current.status = "draft"
    monkeypatch.setattr(dao, "get_agent", AsyncMock(return_value=None if scenario == "missing" else current))
    snapshot = SimpleNamespace(f_version=2, f_prompt_version=1, f_deleted=scenario == "deleted", f_tools=[])
    monkeypatch.setattr(dao, "current_version", AsyncMock(return_value=None if scenario == "no_snapshot" else snapshot))
    with pytest.raises(HTTPException) as failure:
        asyncio.run(get_task_version("studio_smart_qa", Account("owner", "user"), object()))
    assert failure.value.status_code == status
