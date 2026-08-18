"""
Session repository implementation

Implements the session repository interface with SQLAlchemy.
Column names carry the f_ prefix, following the table naming convention.
"""

import time
from typing import List, Optional
from datetime import datetime
from sqlalchemy import select, update, delete, func
from sqlalchemy.ext.asyncio import AsyncSession

from src.domain.repositories.session_repository import ISessionRepository
from src.domain.entities.session import Session
from src.infrastructure.persistence.models.session_model import SessionModel


class SqlSessionRepository(ISessionRepository):
    """
    Session repository implementation

    The infrastructure-layer adapter for the port the domain layer defines.
    """

    def __init__(self, session: AsyncSession, execution_repo=None):
        self._session = session
        self._execution_repo = execution_repo

    async def save(self, session: Session) -> None:
        """Save the session"""
        import json

        model = await self._session.get(SessionModel, session.id)
        now_ms = int(time.time() * 1000)

        if model:
            # Update the existing row
            model.f_template_id = session.template_id
            model.f_status = (
                session.status.value if hasattr(session.status, "value") else session.status
            )
            model.f_runtime_type = session.runtime_type
            model.f_runtime_node = session.runtime_node or ""
            model.f_container_id = session.container_id or ""
            model.f_pod_name = session.pod_name or ""
            model.f_workspace_path = session.workspace_path
            model.f_resources_cpu = session.resource_limit.cpu
            model.f_resources_memory = session.resource_limit.memory
            model.f_resources_disk = session.resource_limit.disk
            model.f_env_vars = (
                json.dumps(session.env_vars, ensure_ascii=False) if session.env_vars else ""
            )
            model.f_timeout = session.timeout
            model.f_python_package_index_url = session.python_package_index_url
            model.f_last_activity_at = (
                int(session.last_activity_at.timestamp() * 1000)
                if session.last_activity_at
                else now_ms
            )
            model.f_updated_at = now_ms
            model.f_completed_at = (
                int(session.completed_at.timestamp() * 1000) if session.completed_at else 0
            )

            # Dependency installation columns
            model.f_requested_dependencies = (
                json.dumps(session.requested_dependencies, ensure_ascii=False)
                if session.requested_dependencies
                else ""
            )
            if session.installed_dependencies:
                deps_list = [
                    {
                        "name": dep.name,
                        "version": dep.version,
                        "install_location": dep.install_location,
                        "install_time": dep.install_time.isoformat(),
                        "is_from_template": dep.is_from_template,
                    }
                    for dep in session.installed_dependencies
                ]
                model.f_installed_dependencies = json.dumps(deps_list, ensure_ascii=False)
            model.f_dependency_install_status = session.dependency_install_status
            model.f_dependency_install_error = session.dependency_install_error or ""
            model.f_dependency_install_started_at = (
                int(session.dependency_install_started_at.timestamp() * 1000)
                if session.dependency_install_started_at
                else 0
            )
            model.f_dependency_install_completed_at = (
                int(session.dependency_install_completed_at.timestamp() * 1000)
                if session.dependency_install_completed_at
                else 0
            )
        else:
            # Insert a new row
            model = SessionModel.from_entity(session)
            self._session.add(model)

        await self._session.flush()

    async def find_by_id(self, session_id: str) -> Optional[Session]:
        """Find a session by id"""
        model = await self._session.get(SessionModel, session_id)
        return model.to_entity() if model else None

    async def find_by_container_id(self, container_id: str) -> Optional[Session]:
        """Find a session by container id"""
        stmt = select(SessionModel).where(SessionModel.f_container_id == container_id)
        result = await self._session.execute(stmt)
        model = result.scalar_one_or_none()
        return model.to_entity() if model else None

    async def find_by_status(self, status: str, limit: int = 100) -> List[Session]:
        """Find sessions by status"""
        stmt = select(SessionModel).where(SessionModel.f_status == status).limit(limit)
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def find_by_template(self, template_id: str) -> List[Session]:
        """Find sessions by template id"""
        stmt = select(SessionModel).where(SessionModel.f_template_id == template_id)
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def find_idle_sessions(self, idle_threshold: datetime) -> List[Session]:
        """Find the idle sessions"""
        threshold_ms = int(idle_threshold.timestamp() * 1000)
        stmt = select(SessionModel).where(
            SessionModel.f_status.in_(["creating", "running"]),
            SessionModel.f_last_activity_at < threshold_ms,
        )
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def find_expired_sessions(self, created_before: datetime) -> List[Session]:
        """Find the expired sessions"""
        before_ms = int(created_before.timestamp() * 1000)
        stmt = (
            select(SessionModel)
            .where(SessionModel.f_created_at < before_ms)
            .where(SessionModel.f_status.in_(["creating", "running"]))
        )
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def delete(self, session_id: str) -> None:
        """
        Delete the session and every execution record it owns, cascading

        The execution rows go first, then the session row.
        """
        # 1. Delete the execution rows first, the cascade
        if self._execution_repo:
            await self._execution_repo.delete_by_session_id(session_id)
        # 2. Then delete the session row
        stmt = delete(SessionModel).where(SessionModel.f_id == session_id)
        await self._session.execute(stmt)
        await self._session.flush()

    async def exists(self, session_id: str) -> bool:
        """Check whether the session exists"""
        model = await self._session.get(SessionModel, session_id)
        return model is not None

    async def count_by_status(self, status: str) -> int:
        """Count the sessions in a status"""
        stmt = select(func.count()).select_from(SessionModel).where(SessionModel.f_status == status)
        result = await self._session.execute(stmt)
        return result.scalar() or 0

    async def count_by_node(self, runtime_node: str) -> int:
        """Count the sessions on a node"""
        stmt = (
            select(func.count())
            .select_from(SessionModel)
            .where(SessionModel.f_runtime_node == runtime_node)
            .where(SessionModel.f_status.in_(["creating", "running"]))
        )
        result = await self._session.execute(stmt)
        return result.scalar() or 0

    async def find_sessions(
        self,
        status: Optional[str] = None,
        template_id: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> List[Session]:
        """
        Find sessions, with filtering and paging

        Args:
            status: filter by session status, optional
            template_id: filter by template id, optional
            limit: how many to return, 1-200, default 50
            offset: offset, for paging

        Returns:
            The session list
        """
        # Validate the limit range
        limit = max(1, min(limit, 200))
        offset = max(0, offset)

        # Build the query
        stmt = select(SessionModel)

        # Apply the filters
        if status:
            stmt = stmt.where(SessionModel.f_status == status)
        if template_id:
            stmt = stmt.where(SessionModel.f_template_id == template_id)

        # Order and page
        stmt = stmt.order_by(SessionModel.f_created_at.desc()).limit(limit).offset(offset)

        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def count_sessions(
        self, status: Optional[str] = None, template_id: Optional[str] = None
    ) -> int:
        """
        Count the sessions, with filtering

        Args:
            status: filter by session status, optional
            template_id: filter by template id, optional

        Returns:
            The total
        """
        stmt = select(func.count()).select_from(SessionModel)

        # Apply the filters
        if status:
            stmt = stmt.where(SessionModel.f_status == status)
        if template_id:
            stmt = stmt.where(SessionModel.f_template_id == template_id)

        result = await self._session.execute(stmt)
        return result.scalar() or 0
