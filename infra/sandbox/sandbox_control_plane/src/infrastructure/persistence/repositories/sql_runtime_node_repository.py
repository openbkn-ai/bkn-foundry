"""
Runtime node repository implementation

Implements the runtime node repository interface with SQLAlchemy.
Column names carry the f_ prefix, following the table naming convention.
"""

import time
from typing import List, Optional
from decimal import Decimal
from sqlalchemy import select, update
from sqlalchemy.ext.asyncio import AsyncSession

from src.domain.repositories.runtime_node_repository import IRuntimeNodeRepository
from src.infrastructure.persistence.models.runtime_node_model import RuntimeNodeModel


class SqlRuntimeNodeRepository(IRuntimeNodeRepository):
    """
    Runtime node repository implementation

    The infrastructure-layer adapter for the port the domain layer defines.
    """

    def __init__(self, session: AsyncSession):
        self._session = session

    async def save(self, node) -> None:
        """Save the node, creating or updating it"""
        import json

        model = await self._session.get(RuntimeNodeModel, node.node_id)
        now_ms = int(time.time() * 1000)

        if model:
            # Update the existing row
            model.f_hostname = node.hostname
            model.f_runtime_type = node.type
            model.f_ip_address = node.ip_address
            model.f_api_endpoint = node.url
            model.f_status = "online"
            model.f_total_cpu_cores = Decimal(str(node.total_cpu_cores))
            model.f_total_memory_mb = node.total_memory_mb
            model.f_max_containers = node.max_sessions
            model.f_cached_images = (
                json.dumps(node.cached_templates, ensure_ascii=False)
                if node.cached_templates
                else "[]"
            )
            model.f_updated_at = now_ms
        else:
            # Insert a new row
            model = RuntimeNodeModel(
                f_node_id=node.node_id,
                f_hostname=node.hostname,
                f_runtime_type=node.type,
                f_ip_address=node.ip_address,
                f_api_endpoint=node.url,
                f_status="online",
                f_total_cpu_cores=Decimal(str(node.total_cpu_cores)),
                f_total_memory_mb=node.total_memory_mb,
                f_max_containers=node.max_sessions,
                f_cached_images=(
                    json.dumps(node.cached_templates, ensure_ascii=False)
                    if node.cached_templates
                    else "[]"
                ),
                f_labels="{}",
                f_running_containers=0,
                f_allocated_cpu_cores=Decimal("0"),
                f_allocated_memory_mb=0,
                f_last_heartbeat_at=now_ms,
                f_created_at=now_ms,
                f_created_by="system",
                f_updated_at=now_ms,
                f_updated_by="system",
                f_deleted_at=0,
                f_deleted_by="",
            )
            self._session.add(model)

        await self._session.flush()

    async def find_by_id(self, node_id: str) -> Optional:
        """Find a node by id"""
        model = await self._session.get(RuntimeNodeModel, node_id)
        return model if model else None

    async def find_by_hostname(self, hostname: str) -> Optional:
        """Find a node by hostname"""
        stmt = select(RuntimeNodeModel).where(RuntimeNodeModel.f_hostname == hostname)
        result = await self._session.execute(stmt)
        return result.scalar_one_or_none()

    async def find_by_status(self, status: str) -> List:
        """Find nodes by status"""
        stmt = select(RuntimeNodeModel).where(RuntimeNodeModel.f_status == status)
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def find_all(self, offset: int = 0, limit: int = 100) -> List:
        """Find every node"""
        stmt = (
            select(RuntimeNodeModel)
            .offset(offset)
            .limit(limit)
            .order_by(RuntimeNodeModel.f_hostname)
        )
        result = await self._session.execute(stmt)
        return list(result.scalars().all())

    async def update_status(self, node_id: str, status: str) -> None:
        """Update the node status"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(f_status=status, f_updated_at=int(time.time() * 1000))
        )
        await self._session.execute(stmt)
        await self._session.flush()

    async def update_heartbeat(self, node_id: str) -> None:
        """Update the node heartbeat time"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(f_last_heartbeat_at=int(time.time() * 1000))
        )
        await self._session.execute(stmt)
        await self._session.flush()

    async def allocate_resources(self, node_id: str, cpu_cores: float, memory_mb: int) -> None:
        """Allocate resources"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(
                f_allocated_cpu_cores=RuntimeNodeModel.f_allocated_cpu_cores
                + Decimal(str(cpu_cores)),
                f_allocated_memory_mb=RuntimeNodeModel.f_allocated_memory_mb + memory_mb,
                f_updated_at=int(time.time() * 1000),
            )
        )
        await self._session.execute(stmt)
        await self._session.flush()

    async def release_resources(self, node_id: str, cpu_cores: float, memory_mb: int) -> None:
        """Release resources"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(
                f_allocated_cpu_cores=RuntimeNodeModel.f_allocated_cpu_cores
                - Decimal(str(cpu_cores)),
                f_allocated_memory_mb=RuntimeNodeModel.f_allocated_memory_mb - memory_mb,
                f_updated_at=int(time.time() * 1000),
            )
        )
        await self._session.execute(stmt)
        await self._session.flush()

    async def increment_container_count(self, node_id: str) -> None:
        """Increment the container count"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(
                f_running_containers=RuntimeNodeModel.f_running_containers + 1,
                f_updated_at=int(time.time() * 1000),
            )
        )
        await self._session.execute(stmt)
        await self._session.flush()

    async def decrement_container_count(self, node_id: str) -> None:
        """Decrement the container count"""
        stmt = (
            update(RuntimeNodeModel)
            .where(RuntimeNodeModel.f_node_id == node_id)
            .values(
                f_running_containers=RuntimeNodeModel.f_running_containers - 1,
                f_updated_at=int(time.time() * 1000),
            )
        )
        await self._session.execute(stmt)
        await self._session.flush()
