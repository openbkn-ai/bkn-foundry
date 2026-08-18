"""
Template REST API routes

Defines the HTTP endpoints for templates.
"""

from fastapi import APIRouter, Depends, HTTPException, status
from typing import List

from src.application.services.template_service import TemplateService
from src.application.commands.create_template import CreateTemplateCommand
from src.application.commands.update_template import UpdateTemplateCommand
from src.application.queries.get_template import GetTemplateQuery
from src.application.dtos.template_dto import TemplateDTO
from src.interfaces.rest.schemas.request import CreateTemplateRequest, UpdateTemplateRequest
from src.interfaces.rest.schemas.response import TemplateResponse, ErrorResponse
from src.infrastructure.dependencies import get_template_service_db

router = APIRouter(prefix="/templates", tags=["templates"])


@router.post("", response_model=TemplateResponse, status_code=status.HTTP_201_CREATED)
async def create_template(
    request: CreateTemplateRequest, service: TemplateService = Depends(get_template_service_db)
):
    """
    Create a template

    - **id**: template id
    - **name**: template name
    - **image_url**: image URL
    - **runtime_type**: runtime type (python3.11, nodejs20, java17, go1.21)
    - **default_cpu_cores**: default CPU cores
    - **default_memory_mb**: default memory in MB
    - **default_disk_mb**: default disk in MB
    - **default_timeout_sec**: default timeout in seconds
    - **default_env_vars**: default environment variables
    """
    command = CreateTemplateCommand(
        template_id=request.id,
        name=request.name,
        image_url=request.image_url,
        runtime_type=request.runtime_type,
        default_cpu_cores=request.default_cpu_cores,
        default_memory_mb=request.default_memory_mb,
        default_disk_mb=request.default_disk_mb,
        default_timeout_sec=request.default_timeout,
        default_env_vars=request.default_env_vars,
    )

    template_dto = await service.create_template(command)
    return _map_dto_to_response(template_dto)


@router.get("", response_model=List[TemplateResponse])
async def list_templates(
    limit: int = 50, offset: int = 0, service: TemplateService = Depends(get_template_service_db)
):
    """List every template"""
    templates = await service.list_templates(limit=limit, offset=offset)
    return [_map_dto_to_response(t) for t in templates]


@router.get("/{template_id}", response_model=TemplateResponse)
async def get_template(
    template_id: str, service: TemplateService = Depends(get_template_service_db)
):
    """Get the template details"""
    query = GetTemplateQuery(template_id=template_id)
    template_dto = await service.get_template(query)
    return _map_dto_to_response(template_dto)


@router.put("/{template_id}", response_model=TemplateResponse)
async def update_template(
    template_id: str,
    request: UpdateTemplateRequest,
    service: TemplateService = Depends(get_template_service_db),
):
    """Update the template"""
    command = UpdateTemplateCommand(
        template_id=template_id,
        name=request.name,
        image_url=request.image_url,
        default_cpu_cores=request.default_cpu_cores,
        default_memory_mb=request.default_memory_mb,
        default_disk_mb=request.default_disk_mb,
        default_timeout_sec=request.default_timeout,
        default_env_vars=request.default_env_vars,
    )

    template_dto = await service.update_template(command)
    return _map_dto_to_response(template_dto)


@router.delete("/{template_id}")
async def delete_template(
    template_id: str, service: TemplateService = Depends(get_template_service_db)
):
    """Delete the template"""
    await service.delete_template(template_id)
    return {"message": "Template deleted successfully"}


def _map_dto_to_response(dto: TemplateDTO) -> TemplateResponse:
    """Map a TemplateDTO onto a TemplateResponse"""
    return TemplateResponse(
        id=dto.id,
        name=dto.name,
        image_url=dto.image_url,
        runtime_type=dto.runtime_type,
        default_cpu_cores=dto.default_cpu_cores,
        default_memory_mb=dto.default_memory_mb,
        default_disk_mb=dto.default_disk_mb,
        default_timeout_sec=dto.default_timeout_sec,
        default_env_vars=dto.default_env_vars,
        is_active=dto.is_active,
        created_at=dto.created_at,
        updated_at=dto.updated_at,
    )
