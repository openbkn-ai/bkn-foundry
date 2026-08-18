"""
Template repository implementation

Implements the template repository interface with SQLAlchemy.
Column names carry the f_ prefix, following the table naming convention.
"""

import re
import json
import time
from typing import List
from decimal import Decimal
from sqlalchemy import select, delete, func
from sqlalchemy.ext.asyncio import AsyncSession

from src.domain.repositories.template_repository import ITemplateRepository
from src.domain.entities.template import Template
from src.infrastructure.persistence.models.template_model import TemplateModel


class SqlTemplateRepository(ITemplateRepository):
    """
    Template repository implementation

    The infrastructure-layer adapter for the port the domain layer defines.
    """

    def __init__(self, session: AsyncSession):
        self._session = session

    async def save(self, template: Template) -> None:
        """Save the template"""
        model = await self._session.get(TemplateModel, template.id)
        now_ms = int(time.time() * 1000)

        def parse_mb_value(value: str) -> int:
            """Parse a resource value, turning '512Mi' or '1Gi' into MB"""
            if not value:
                return 512  # default

            # Take the numeric part
            numeric_str = re.sub(r"[^0-9.]", "", value)
            if not numeric_str:
                return 512

            numeric = float(numeric_str)

            # Convert by unit
            if "Gi" in value or "GB" in value or "G" in value:
                return int(numeric * 1024)
            elif "Mi" in value or "MB" in value or "M" in value:
                return int(numeric)
            elif "Ki" in value or "KB" in value or "K" in value:
                return int(numeric / 1024)
            else:
                # Without a unit, assume MB
                return int(numeric)

        if model:
            # Update the existing row
            model.f_name = template.name
            model.f_description = ""
            model.f_image_url = template.image
            model.f_base_image = template.base_image
            model.f_pre_installed_packages = (
                json.dumps(template.pre_installed_packages, ensure_ascii=False)
                if template.pre_installed_packages
                else "[]"
            )
            model.f_runtime_type = "python3.11"  # Default, should be from entity
            model.f_default_cpu_cores = Decimal(template.default_resources.cpu)
            model.f_default_memory_mb = parse_mb_value(template.default_resources.memory)
            model.f_default_disk_mb = parse_mb_value(template.default_resources.disk)
            model.f_default_timeout_sec = template.default_timeout_sec
            model.f_security_context = (
                json.dumps(template.security_context, ensure_ascii=False)
                if template.security_context
                else "{}"
            )
            model.f_updated_at = now_ms
        else:
            # Insert a new row
            model = TemplateModel.from_entity(template)
            self._session.add(model)

        await self._session.flush()

    async def find_by_id(self, template_id: str) -> Template | None:
        """Find a template by id"""
        model = await self._session.get(TemplateModel, template_id)
        return model.to_entity() if model else None

    async def find_by_name(self, name: str) -> Template | None:
        """Find a template by name"""
        stmt = select(TemplateModel).where(TemplateModel.f_name == name)
        result = await self._session.execute(stmt)
        model = result.scalar_one_or_none()
        return model.to_entity() if model else None

    async def find_all(self, offset: int = 0, limit: int = 100) -> List[Template]:
        """Find every template"""
        stmt = select(TemplateModel).offset(offset).limit(limit).order_by(TemplateModel.f_name)
        result = await self._session.execute(stmt)
        return [model.to_entity() for model in result.scalars().all()]

    async def delete(self, template_id: str) -> None:
        """Delete the template"""
        stmt = delete(TemplateModel).where(TemplateModel.f_id == template_id)
        await self._session.execute(stmt)
        await self._session.flush()

    async def exists(self, template_id: str) -> bool:
        """Check whether the template exists"""
        model = await self._session.get(TemplateModel, template_id)
        return model is not None

    async def exists_by_name(self, name: str) -> bool:
        """Check whether the name exists"""
        stmt = select(func.count()).select_from(TemplateModel).where(TemplateModel.f_name == name)
        result = await self._session.execute(stmt)
        return (result.scalar() or 0) > 0

    async def count(self) -> int:
        """Count the templates"""
        stmt = select(func.count()).select_from(TemplateModel)
        result = await self._session.execute(stmt)
        return result.scalar() or 0
