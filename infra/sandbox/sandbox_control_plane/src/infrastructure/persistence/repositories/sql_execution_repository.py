"""
Execution repository implementation

Implements the execution repository interface with SQLAlchemy.
Column names carry the f_ prefix, following the table naming convention.
"""

import time
from typing import List, Optional
from datetime import datetime
from sqlalchemy import select, update, delete, func
from sqlalchemy.ext.asyncio import AsyncSession

from src.domain.repositories.execution_repository import IExecutionRepository
from src.domain.entities.execution import Execution
from src.infrastructure.persistence.models.execution_model import ExecutionModel


class SqlExecutionRepository(IExecutionRepository):
    """
    Execution repository implementation

    The infrastructure-layer adapter for the port the domain layer defines.
    """

    def __init__(self, session: AsyncSession):
        self._session = session

    async def save(self, execution: Execution) -> None:
        """Save the execution record"""
        import json

        model = await self._session.get(ExecutionModel, execution.id)
        now_ms = int(time.time() * 1000)

        if model:
            # Update the existing row
            model.f_session_id = execution.session_id
            model.f_code = execution.code
            model.f_language = execution.language
            model.f_status = execution.state.status.value
            model.f_stdout = execution.stdout
            model.f_stderr = execution.stderr
            model.f_exit_code = execution.state.exit_code or 0
            model.f_return_value = (
                json.dumps(execution.return_value, ensure_ascii=False)
                if execution.return_value
                else ""
            )
            model.f_metrics = (
                json.dumps(execution.metrics, ensure_ascii=False) if execution.metrics else ""
            )
            model.f_error_message = execution.state.error_message or ""
            model.f_completed_at = (
                int(execution.completed_at.timestamp() * 1000) if execution.completed_at else 0
            )
            model.f_updated_at = now_ms
        else:
            # Insert a new row
            model = ExecutionModel.from_entity(execution)
            self._session.add(model)

        await self._session.flush()

    async def commit(self) -> None:
        """Explicitly commit the transaction"""
        await self._session.commit()

    async def find_by_id(self, execution_id: str) -> Optional[Execution]:
        """Find an execution record by id"""
        # Use a fresh query to avoid stale data from session cache
        # This is important for the sync execution polling loop
        stmt = select(ExecutionModel).where(ExecutionModel.f_id == execution_id)
        result = await self._session.execute(stmt)
        model = result.scalar_one_or_none()
        return model.to_entity() if model else None

    async def find_by_session_id(self, session_id: str, limit: int = 100) -> List[Execution]:
        """Find the execution records of a session"""
        stmt = (
            select(ExecutionModel)
            .where(ExecutionModel.f_session_id == session_id)
            .order_by(ExecutionModel.f_created_at.desc())
            .limit(limit)
        )
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def find_by_status(self, status: str, limit: int = 100) -> List[Execution]:
        """Find execution records by status"""
        stmt = select(ExecutionModel).where(ExecutionModel.f_status == status).limit(limit)
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def find_crashed_executions(self, max_retry_count: int) -> List[Execution]:
        """Find the crashed executions that may be retried"""
        # retry_count is not on the database model, so this returns an empty list.
        # Supporting it needs an f_retry_count column on ExecutionModel.
        return []

    async def find_heartbeat_timeouts(self, timeout_threshold: datetime) -> List[Execution]:
        """Find the executions whose heartbeat timed out"""
        # last_heartbeat_at is not on the database model
        return []

    async def delete(self, execution_id: str) -> None:
        """Delete the execution record"""
        stmt = delete(ExecutionModel).where(ExecutionModel.f_id == execution_id)
        await self._session.execute(stmt)
        await self._session.flush()

    async def delete_by_session_id(self, session_id: str) -> None:
        """Delete every execution record of a session"""
        stmt = delete(ExecutionModel).where(ExecutionModel.f_session_id == session_id)
        await self._session.execute(stmt)
        await self._session.flush()

    async def count_by_status(self, status: str) -> int:
        """Count the executions in a status"""
        stmt = (
            select(func.count())
            .select_from(ExecutionModel)
            .where(ExecutionModel.f_status == status)
        )
        result = await self._session.execute(stmt)
        return result.scalar() or 0
