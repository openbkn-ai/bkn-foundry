"""
Template application service

Orchestrates the template use cases.
"""

from typing import List, Optional

from src.domain.entities.template import Template
from src.domain.repositories.template_repository import ITemplateRepository
from src.application.commands.create_template import CreateTemplateCommand
from src.application.commands.update_template import UpdateTemplateCommand
from src.application.queries.get_template import GetTemplateQuery
from src.application.dtos.template_dto import TemplateDTO
from src.shared.errors.domain import NotFoundError, ValidationError
from src.shared.i18n import message


class TemplateService:
    """
    Template application service

    Orchestrates creating, reading, updating, and deleting a template.
    """

    def __init__(
        self,
        template_repo: ITemplateRepository,
    ):
        self._template_repo = template_repo

    async def create_template(self, command: CreateTemplateCommand) -> TemplateDTO:
        """
        Create-template use case

        Steps:
        1. Check the template id is unique
        2. Check the template name is unique
        3. Build the template entity
        4. Persist it
        """
        # Check the id is unique
        existing_by_id = await self._template_repo.find_by_id(command.template_id)
        if existing_by_id:
            raise ValidationError(message("Sandbox.Template.IdExists", template_id=command.template_id))

        # Check the name is unique
        existing = await self._template_repo.find_by_name(command.name)
        if existing:
            raise ValidationError(message("Sandbox.Template.NameExists", name=command.name))

        from src.domain.value_objects.resource_limit import ResourceLimit

        template = Template(
            id=command.template_id,
            name=command.name,
            image=command.image_url,
            base_image=command.image_url,
            pre_installed_packages=[],
            default_resources=ResourceLimit(
                cpu=str(command.default_cpu_cores),
                memory=f"{command.default_memory_mb}Mi",
                disk=f"{command.default_disk_mb}Mi",
                max_processes=128,
            ),
            default_timeout_sec=command.default_timeout_sec or 300,
            security_context={},
        )

        await self._template_repo.save(template)
        return TemplateDTO.from_entity(template)

    async def get_template(self, query: GetTemplateQuery) -> TemplateDTO:
        """Get-template use case"""
        template = await self._template_repo.find_by_id(query.template_id)
        if not template:
            raise NotFoundError(message("Sandbox.Template.NotFound", template_id=query.template_id))

        return TemplateDTO.from_entity(template)

    async def list_templates(self, limit: int = 50, offset: int = 0) -> List[TemplateDTO]:
        """List every template"""
        templates = await self._template_repo.find_all(limit=limit, offset=offset)

        return [TemplateDTO.from_entity(t) for t in templates]

    async def update_template(self, command: UpdateTemplateCommand) -> TemplateDTO:
        """
        Update-template use case

        Steps:
        1. Find the template
        2. Check the name is unique, when the name changes
        3. Update the fields
        4. Persist it
        """
        template = await self._template_repo.find_by_id(command.template_id)
        if not template:
            raise NotFoundError(message("Sandbox.Template.NotFound", template_id=command.template_id))

        # Check the name is unique
        if command.name and command.name != template.name:
            existing = await self._template_repo.find_by_name(command.name)
            if existing and existing.id != template.id:
                raise ValidationError(message("Sandbox.Template.NameExists", name=command.name))

        # Update the name
        if command.name is not None:
            template.update_name(command.name)

        # Update the image
        if command.image_url is not None:
            template.update_image(command.image_url)

        # Update the resource limits
        if any([command.default_cpu_cores, command.default_memory_mb, command.default_disk_mb]):
            from src.domain.value_objects.resource_limit import ResourceLimit

            cpu = (
                str(command.default_cpu_cores)
                if command.default_cpu_cores
                else template.default_resources.cpu
            )
            memory = (
                f"{command.default_memory_mb}Mi"
                if command.default_memory_mb
                else template.default_resources.memory
            )
            disk = (
                f"{command.default_disk_mb}Mi"
                if command.default_disk_mb
                else template.default_resources.disk
            )

            template.default_resources = ResourceLimit(
                cpu=cpu,
                memory=memory,
                disk=disk,
                max_processes=template.default_resources.max_processes,
            )

        # Update the timeout
        if command.default_timeout_sec is not None:
            template.update_timeout(command.default_timeout_sec)

        await self._template_repo.save(template)
        return TemplateDTO.from_entity(template)

    async def delete_template(self, template_id: str) -> None:
        """
        Delete-template use case

        Steps:
        1. Find the template
        2. Check no active session uses it
        3. Delete it
        """
        template = await self._template_repo.find_by_id(template_id)
        if not template:
            raise NotFoundError(message("Sandbox.Template.NotFound", template_id=template_id))

        await self._template_repo.delete(template_id)
