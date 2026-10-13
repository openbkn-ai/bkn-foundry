# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Regression coverage for result callbacks that previously left executions running."""

from copy import deepcopy
from unittest.mock import AsyncMock

import pytest
from sqlalchemy.dialects import mysql
from sqlalchemy.schema import CreateTable

from src.domain.entities.execution import Execution
from src.domain.value_objects.execution_status import ExecutionState, ExecutionStatus
from src.infrastructure.config.settings import Settings
from src.infrastructure.persistence.database import Base  # noqa: F401
from src.infrastructure.persistence.models.execution_model import ExecutionModel
from src.interfaces.rest.api.v1 import internal
from src.interfaces.rest.schemas.internal import ExecutionResultReport


@pytest.fixture
def repository(monkeypatch):
    monkeypatch.setattr(internal, "get_settings", lambda: Settings(debug=False))
    execution = Execution(
        id="exec-result",
        session_id="session",
        code="print('ok')",
        language="python",
        state=ExecutionState(status=ExecutionStatus.RUNNING),
    )
    return AsyncMock(find_by_id=AsyncMock(return_value=execution))


@pytest.mark.asyncio
@pytest.mark.parametrize("field", ["stdout", "stderr", "return_value"])
async def test_result_larger_than_text_capacity_is_preserved(repository, field):
    value = "中文" * 20000
    report = ExecutionResultReport(
        status="failed" if field == "stderr" else "success",
        exit_code=1 if field == "stderr" else 0,
        execution_time=0.2,
        **{field: {"value": value} if field == "return_value" else value},
    )
    response = await internal.report_execution_result("exec-result", report, repository)
    assert response.status_code == 201
    saved = repository.save.call_args.args[0]
    assert getattr(saved, field) == getattr(report, field)
    if field == "stderr":
        assert len(saved.state.error_message.encode("utf-8")) < 65536


def test_mysql_result_columns_have_mediumtext_capacity():
    ddl = str(CreateTable(ExecutionModel.__table__).compile(dialect=mysql.dialect()))
    for field in ("f_stdout", "f_stderr", "f_return_value"):
        assert f"{field} MEDIUMTEXT" in ddl


@pytest.mark.asyncio
async def test_aggregate_limit_counts_utf8_and_finishes_with_explicit_failure(
    repository, monkeypatch
):
    monkeypatch.setattr(
        internal, "get_settings", lambda: Settings(debug=False, max_execution_result_bytes=100)
    )
    report = ExecutionResultReport(
        status="success",
        exit_code=0,
        execution_time=0.2,
        stdout="中" * 20,
        stderr="x" * 50,
    )
    await internal.report_execution_result("exec-result", report, repository)
    saved = repository.save.call_args.args[0]
    assert saved.state.status == ExecutionStatus.FAILED
    assert saved.state.exit_code != 0
    assert "Sandbox.Execution.OutputLimitExceeded" in saved.state.error_message
    assert saved.stdout == ""
    assert saved.stderr == saved.state.error_message
    assert saved.return_value is None
    repeat = await internal.report_execution_result("exec-result", report, repository)
    assert repeat.message == "Result already recorded"
    assert repository.save.await_count == 1


@pytest.mark.asyncio
async def test_failed_transaction_is_rolled_back_before_recording_terminal_failure(repository):
    pending = deepcopy(repository.find_by_id.return_value)
    repository.find_by_id.side_effect = [repository.find_by_id.return_value, pending]
    repository.save.side_effect = [RuntimeError("sensitive SQL payload"), None]
    report = ExecutionResultReport(status="success", exit_code=0, execution_time=0.2, stdout="ok")
    response = await internal.report_execution_result("exec-result", report, repository)
    assert response.status_code == 201
    repository.rollback.assert_awaited_once()
    saved = repository.save.call_args.args[0]
    assert saved.state.status == ExecutionStatus.FAILED
    assert "Sandbox.Execution.ResultPersistenceFailed" in saved.state.error_message
    assert "sensitive" not in saved.state.error_message
    assert saved.stdout == ""
    assert saved.stderr == saved.state.error_message


@pytest.mark.asyncio
async def test_database_outage_remains_retryable_without_exposing_sql(repository, caplog):
    repository.save.side_effect = RuntimeError("sensitive SQL payload")
    repository.rollback.side_effect = RuntimeError("sensitive connection details")
    report = ExecutionResultReport(status="success", exit_code=0, execution_time=0.2)
    with pytest.raises(internal.HTTPException) as caught:
        await internal.report_execution_result("exec-result", report, repository)
    assert caught.value.status_code == 500
    assert "Sandbox.Execution.ResultPersistenceFailed" in caught.value.detail
    assert "sensitive" not in caught.value.detail + caplog.text


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "limit, expected", [(22, ExecutionStatus.COMPLETED), (21, ExecutionStatus.FAILED)]
)
async def test_serialized_return_counts_toward_aggregate_utf8_limit(
    repository, monkeypatch, limit, expected
):
    monkeypatch.setattr(
        internal, "get_settings", lambda: Settings(debug=False, max_execution_result_bytes=limit)
    )
    report = ExecutionResultReport(
        status="success",
        exit_code=0,
        execution_time=0.2,
        stdout="中",
        stderr="x" * 3,
        return_value={"value": "中"},
    )
    # 3 + 3 + 16 serialized UTF-8 bytes: exactly 22 is accepted.
    await internal.report_execution_result("exec-result", report, repository)
    assert repository.save.call_args.args[0].state.status == expected
