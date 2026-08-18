"""
Session REST API routes

Defines the HTTP endpoints for sessions.
"""

from fastapi import APIRouter, Depends, HTTPException, status, Response
from typing import List, Optional

from src.application.commands.install_session_dependencies import (
    InstallSessionDependenciesCommand,
)
from src.application.services.session_service import SessionService
from src.shared.errors.domain import ConflictError
from src.application.commands.create_session import CreateSessionCommand
from src.application.commands.execute_code import ExecuteCodeCommand
from src.application.dtos.session_dto import SessionDTO
from src.infrastructure.executors.errors import (
    ExecutorConnectionError,
    ExecutorResponseError,
    ExecutorTimeoutError,
    ExecutorUnavailableError,
    ExecutorValidationError,
)
from src.interfaces.rest.schemas.request import (
    CreateSessionRequest,
    ExecuteCodeRequest,
    InstallSessionDependenciesRequest,
)
from src.interfaces.rest.schemas.response import (
    DependencyResponse,
    InstalledDependencyResponse,
    SessionResponse,
    SessionListResponse,
    ExecuteCodeResponse,
    ErrorResponse,
)
from src.infrastructure.dependencies import get_session_service_db

router = APIRouter(prefix="/sessions", tags=["sessions"])


@router.post("", response_model=SessionResponse, status_code=status.HTTP_201_CREATED)
async def create_session(
    request: CreateSessionRequest, service: SessionService = Depends(get_session_service_db)
):
    """
    Create a session

    - **template_id**: template id; without it DEFAULT_TEMPLATE_ID applies
    - **timeout**: timeout in seconds, 300 by default, 3600 at most
    - **cpu**: CPU cores, such as "1" or "2"
    - **memory**: memory limit, such as "512Mi" or "1Gi"
    - **disk**: disk limit, such as "1Gi" or "10Gi"
    - **env_vars**: environment variables
    - **dependencies**: session-level dependency packages
    - **install_timeout**: dependency install timeout in seconds, 300 by default
    - **fail_on_dependency_error**: whether a failed dependency install aborts session creation
    - **allow_version_conflicts**: whether a version conflict is allowed
    """
    from src.domain.value_objects.resource_limit import ResourceLimit

    try:
        resource_limit = ResourceLimit(cpu=request.cpu, memory=request.memory, disk=request.disk)

        dependencies_pip_specs = [dep.to_pip_spec() for dep in request.dependencies]

        command = CreateSessionCommand(
            id=request.id,
            template_id=request.template_id,
            timeout=request.timeout,
            resource_limit=resource_limit,
            env_vars=request.env_vars,
            python_package_index_url=request.python_package_index_url,
            dependencies=dependencies_pip_specs,
            install_timeout=request.install_timeout,
            fail_on_dependency_error=request.fail_on_dependency_error,
            allow_version_conflicts=request.allow_version_conflicts,
        )

        session_dto = await service.create_session(command)
        return _map_dto_to_response(session_dto)

    except ConflictError as e:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=str(e))
    except Exception as e:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(e))


@router.get("", response_model=SessionListResponse)
async def list_sessions(
    status: Optional[str] = None,
    template_id: Optional[str] = None,
    limit: int = 50,
    offset: int = 0,
    service: SessionService = Depends(get_session_service_db),
):
    """
    List sessions

    Filters by status and template_id, and pages.

    - **status**: filter by session status, optional, such as "running" or "terminated"
    - **template_id**: filter by template id, optional
    - **limit**: how many to return, 1-200, default 50
    - **offset**: offset, for paging, default 0
    """
    result = await service.list_sessions(
        status=status, template_id=template_id, limit=limit, offset=offset
    )

    return SessionListResponse(
        items=[_map_dto_to_response(item) for item in result["items"]],
        total=result["total"],
        limit=result["limit"],
        offset=result["offset"],
        has_more=result["has_more"],
    )


@router.get("/{session_id}", response_model=SessionResponse)
async def get_session(session_id: str, service: SessionService = Depends(get_session_service_db)):
    """Get the session details"""
    from src.application.queries.get_session import GetSessionQuery

    query = GetSessionQuery(session_id=session_id)
    session_dto = await service.get_session(query)
    return _map_dto_to_response(session_dto)


@router.post(
    "/{session_id}/dependencies/install",
    response_model=SessionResponse,
)
async def install_session_dependencies(
    session_id: str,
    request: InstallSessionDependenciesRequest,
    service: SessionService = Depends(get_session_service_db),
):
    """Install Python dependencies incrementally."""
    try:
        command = InstallSessionDependenciesCommand(
            session_id=session_id,
            python_package_index_url=request.python_package_index_url,
            dependencies=[dep.to_pip_spec() for dep in request.dependencies],
            install_timeout=request.install_timeout,
        )
        session_dto = await service.install_session_dependencies(command)
        return _map_dto_to_response(session_dto)
    except ConflictError as e:
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail=str(e))
    except (ExecutorUnavailableError, ExecutorConnectionError, ExecutorTimeoutError) as e:
        raise HTTPException(status_code=status.HTTP_503_SERVICE_UNAVAILABLE, detail=str(e))
    except (ExecutorValidationError, ExecutorResponseError) as e:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(e))


@router.post("/{session_id}/terminate", response_model=SessionResponse)
async def terminate_session(
    session_id: str, service: SessionService = Depends(get_session_service_db)
):
    """
    Terminate a session: a soft stop that keeps the record

    What happens:
    1. The container is destroyed
    2. The S3 workspace files are deleted
    3. The session moves to terminated
    4. The database record is kept
    """
    session_dto = await service.terminate_session(session_id)
    return _map_dto_to_response(session_dto)


@router.delete("/{session_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_session(
    session_id: str, service: SessionService = Depends(get_session_service_db)
):
    """
    Delete a session: a hard delete that cascades to the execution records

    What happens:
    1. Clean up: destroy the container and delete the S3 files
    2. Hard-delete the session row
    3. Cascade-delete every execution row that belongs to it
    """
    await service.delete_session(session_id)
    return Response(status_code=status.HTTP_204_NO_CONTENT)


def _map_dto_to_response(dto: SessionDTO) -> SessionResponse:
    """Map a SessionDTO onto a SessionResponse"""
    return SessionResponse(
        id=dto.id,
        template_id=dto.template_id,
        status=dto.status,
        resource_limit=dto.resource_limit,
        workspace_path=dto.workspace_path,
        language_runtime=dto.language_runtime,
        runtime_type=dto.runtime_type,
        runtime_node=dto.runtime_node,
        container_id=dto.container_id,
        pod_name=dto.pod_name,
        env_vars=dto.env_vars,
        timeout=dto.timeout,
        python_package_index_url=dto.python_package_index_url,
        requested_dependencies=[DependencyResponse(**dep) for dep in dto.requested_dependencies],
        installed_dependencies=[
            InstalledDependencyResponse(**dep) for dep in dto.installed_dependencies
        ],
        dependency_install_status=dto.dependency_install_status,
        dependency_install_error=dto.dependency_install_error,
        dependency_install_started_at=dto.dependency_install_started_at,
        dependency_install_completed_at=dto.dependency_install_completed_at,
        created_at=dto.created_at,
        updated_at=dto.updated_at,
        completed_at=dto.completed_at,
        last_activity_at=dto.last_activity_at,
    )
