# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Run only against a disposable MySQL using SANDBOX_RESULT_TEST_DATABASE_URL."""

import os
from pathlib import Path

import pytest
import pytest_asyncio
from sqlalchemy import text
from sqlalchemy.engine import make_url
from sqlalchemy.ext.asyncio import async_sessionmaker, create_async_engine
from sqlalchemy.schema import CreateTable

from src.domain.entities.execution import Execution
from src.domain.value_objects.execution_status import ExecutionState, ExecutionStatus
from src.infrastructure.persistence.database import (  # noqa: F401
    LEGACY_DATABASE_NAME,
    TARGET_DATABASE_NAME,
    Base,
)
from src.infrastructure.persistence.models.execution_model import ExecutionModel
from src.infrastructure.persistence.repositories.sql_execution_repository import (
    SqlExecutionRepository,
)
from src.interfaces.rest.api.v1.internal import report_execution_result
from src.interfaces.rest.schemas.internal import ExecutionResultReport

pytestmark = pytest.mark.integration


@pytest_asyncio.fixture
async def engine():
    url = os.environ.get("SANDBOX_RESULT_TEST_DATABASE_URL")
    if not url:
        pytest.skip("disposable MySQL URL is required")
    # These tests drop and recreate t_sandbox_execution. Refuse the product schemas
    # outright rather than trusting the caller to have picked a scratch database.
    if make_url(url).database in (TARGET_DATABASE_NAME, LEGACY_DATABASE_NAME):
        pytest.fail(f"refusing to run against the product database {make_url(url).database!r}")
    engine = create_async_engine(url)
    async with engine.begin() as conn:
        await conn.run_sync(ExecutionModel.__table__.drop, checkfirst=True)
        legacy = str(CreateTable(ExecutionModel.__table__).compile(dialect=engine.dialect))
        await conn.execute(text(legacy.replace("MEDIUMTEXT", "TEXT")))
    yield engine
    async with engine.begin() as conn:
        await conn.run_sync(ExecutionModel.__table__.drop)
    await engine.dispose()


async def record(engine, field):
    async with async_sessionmaker(engine, expire_on_commit=False)() as session:
        repo = SqlExecutionRepository(session)
        await repo.save(
            Execution(
                id="exec-mysql",
                session_id="session",
                code="print('ok')",
                language="python",
                state=ExecutionState(status=ExecutionStatus.RUNNING),
            )
        )
        await repo.commit()
        value = "中文" * 20000
        report = ExecutionResultReport(
            status="failed" if field == "stderr" else "success",
            exit_code=1 if field == "stderr" else 0,
            execution_time=0.2,
            **{field: {"value": value} if field == "return_value" else value},
        )
        response = await report_execution_result("exec-mysql", report, repo)
        assert response.status_code == 201
        return await repo.find_by_id("exec-mysql"), report


@pytest.mark.asyncio
async def test_legacy_capacity_failure_rolls_back_and_finishes(engine):
    saved, _ = await record(engine, "stdout")
    assert saved.state.status == ExecutionStatus.FAILED
    assert "Sandbox.Execution.ResultPersistenceFailed" in saved.state.error_message
    assert saved.stdout == ""
    assert saved.stderr == saved.state.error_message


@pytest.mark.asyncio
@pytest.mark.parametrize("field", ["stdout", "stderr", "return_value"])
async def test_migration_preserves_large_utf8_results(engine, field):
    root = Path(__file__).resolve().parents[6]
    migration = root / "migrations/sandbox/mariadb/0.2.0/01-expand-execution-results.sql"
    async with engine.begin() as conn:
        for statement in migration.read_text().split(";"):
            # Migrations pin `USE openbkn;` for the installer. Executed here it would
            # move the connection out of the scratch database, and the fixture's
            # DROP TABLE would land on whatever schema the server calls openbkn.
            body = "\n".join(
                line for line in statement.splitlines() if not line.lstrip().startswith("--")
            ).strip()
            if body and not body.upper().startswith("USE "):
                await conn.execute(text(body))
    saved, report = await record(engine, field)
    assert getattr(saved, field) == getattr(report, field)
    assert saved.state.status == (
        ExecutionStatus.FAILED if field == "stderr" else ExecutionStatus.COMPLETED
    )


@pytest.mark.asyncio
async def test_oversized_code_and_credentials_do_not_block_the_record(engine):
    # #2294: run_code prepends a ~64 KB stub, which overflowed the TEXT f_code column
    # with 1406 before the code ever ran. The event carried the caller's token.
    user_code = "print('业务代码')\n"
    code = '"""文档字符串"""\n' * 6000 + user_code
    async with async_sessionmaker(engine, expire_on_commit=False)() as session:
        repo = SqlExecutionRepository(session)
        await repo.save(
            Execution(
                id="exec-mysql",
                session_id="session",
                code=code,
                language="python",
                event_data={"token": "ory_at_secret"},
                state=ExecutionState(status=ExecutionStatus.PENDING),
            )
        )
        await repo.commit()
        saved = await repo.find_by_id("exec-mysql")
        stored_event = (
            await session.execute(text("SELECT f_event_data FROM t_sandbox_execution"))
        ).scalar_one()

    assert len(code.encode("utf-8")) > 65535
    assert saved.code.endswith(user_code)
    assert "ory_at_secret" not in stored_event
