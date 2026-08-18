"""
Internal API routes

The endpoints the executor calls.
They are reachable only from inside the container network.
"""

import logging
from datetime import datetime
from typing import Dict

from fastapi import APIRouter, Depends, HTTPException, status
from fastapi.responses import JSONResponse

from src.domain.repositories.execution_repository import IExecutionRepository
from src.domain.value_objects.execution_status import ExecutionStatus
from src.domain.value_objects.artifact import Artifact, ArtifactType
from src.interfaces.rest.schemas.internal import (
    ContainerReadyRequest,
    ExecutionResultReport,
    InternalAPIResponse,
)
from src.infrastructure.dependencies import (
    USE_SQL_REPOSITORIES,
    get_execution_repository as get_sql_execution_repository,
    get_session_repository as get_sql_session_repository,
)
from src.shared.i18n import message

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/internal", tags=["internal"])


# Pick the dependency injection functions by mode.
# SQL mode: get_execution_repository, which injects the database session through Depends().
# Mock mode: functions that read the repositories off app.state.
if USE_SQL_REPOSITORIES:
    _get_execution_repository = get_sql_execution_repository
    _get_session_repository = get_sql_session_repository
else:
    from src.infrastructure.dependencies import (
        get_execution_repository as get_mock_execution_repository,
    )
    from src.infrastructure.dependencies import (
        get_session_repository as get_mock_session_repository,
    )

    _get_execution_repository = get_mock_execution_repository
    _get_session_repository = get_mock_session_repository


@router.post("/containers/ready")
async def handle_container_ready(
    request: ContainerReadyRequest,
    session_repo=Depends(_get_session_repository),
):
    """
    Handle the container-ready event

    The executor calls this once it has started, telling the control plane the container is ready.
    Moves the matching session to RUNNING.
    """
    logger.info(f"Container ready event received: container_id={request.container_id}")

    # Find the session
    session = await session_repo.find_by_container_id(request.container_id)
    if session:
        # Move the session to RUNNING
        from src.domain.value_objects.execution_status import SessionStatus

        session.status = SessionStatus.RUNNING

        await session_repo.save(session)
        logger.info(f"Session {session.id} status updated to RUNNING")
        return InternalAPIResponse(message="Container ready acknowledged, session updated")
    else:
        logger.warning(f"No session found for container_id={request.container_id}")
        return InternalAPIResponse(message="Container ready acknowledged (no session found)")


@router.post("/containers/exited")
async def handle_container_exited():
    """
    Handle the container-exit event

    The executor calls this before shutting down, telling the control plane the container is about to exit.
    """
    logger.info("Container exited event received")
    # Currently just acknowledge - future: update container status in database
    return InternalAPIResponse(message="Container exited acknowledged")


@router.post("/executions/{execution_id}/heartbeat")
async def handle_execution_heartbeat(execution_id: str):
    """
    Handle an execution heartbeat

    The executor calls this periodically while running, keeping the execution marked active.
    """
    logger.debug(f"Heartbeat received for execution {execution_id}")
    # Currently just acknowledge - future: update last_heartbeat timestamp
    return InternalAPIResponse(message="Heartbeat acknowledged")


@router.post(
    "/executions/{execution_id}/result",
    response_model=InternalAPIResponse,
    status_code=status.HTTP_200_OK,
)
async def report_execution_result(
    execution_id: str,
    report: ExecutionResultReport,
    execution_repo: IExecutionRepository = Depends(_get_execution_repository),
):
    """
    Report an execution result

    The executor calls this once execution finishes, reporting the result to the control plane.

    ## Status mapping
    - API: `"success"` → Domain: `ExecutionStatus.COMPLETED`
    - API: `"failed"` → Domain: `ExecutionStatus.FAILED`
    - API: `"timeout"` → Domain: `ExecutionStatus.TIMEOUT`
    - API: `"crashed"` → Domain: `ExecutionStatus.CRASHED`

    ## Idempotency
    - When the execution record is already terminal, answer 200; this is a repeat report
    - On the first report, update and answer 201
    """
    # 1. Find the execution record
    execution = await execution_repo.find_by_id(execution_id)
    if not execution:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail=message("Sandbox.Execution.NotFound", execution_id=execution_id),
        )

    # 2. Check whether it is already terminal, for idempotency
    if execution.is_terminal():
        logger.info(f"Execution {execution_id} already in terminal state: {execution.state.status}")
        return InternalAPIResponse(message="Result already recorded")

    # 3. Map the API status onto the domain status
    status_map: Dict[str, ExecutionStatus] = {
        "success": ExecutionStatus.COMPLETED,
        "failed": ExecutionStatus.FAILED,
        "timeout": ExecutionStatus.TIMEOUT,
        "crashed": ExecutionStatus.CRASHED,
    }

    domain_status = status_map.get(report.status)
    if not domain_status:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=message("Sandbox.Execution.InvalidStatus", status=report.status),
        )

    # 4. Move PENDING to RUNNING automatically when needed.
    # The domain rule is PENDING -> RUNNING -> COMPLETED/FAILED/TIMEOUT/CRASHED,
    # but the executor may report a finished result, so that step happens here.
    if execution.state.status == ExecutionStatus.PENDING:
        execution.mark_running()

    # 5. Update the execution entity for the reported status
    try:
        if domain_status == ExecutionStatus.COMPLETED:
            # Turn the artifact strings into Artifact objects
            now = datetime.now()
            artifact_objects = [
                Artifact(
                    path=path, size=0, mime_type="", type=ArtifactType.ARTIFACT, created_at=now
                )
                for path in report.artifacts
            ]

            # Convert the metrics
            metrics_dict = None
            if report.metrics:
                metrics_dict = {
                    "duration_ms": report.metrics.duration_ms,
                    "cpu_time_ms": report.metrics.cpu_time_ms,
                    "peak_memory_mb": report.metrics.peak_memory_mb,
                    "io_read_bytes": report.metrics.io_read_bytes,
                    "io_write_bytes": report.metrics.io_write_bytes,
                }

            execution.mark_completed(
                stdout=report.stdout,
                stderr=report.stderr,
                exit_code=report.exit_code,
                execution_time=report.execution_time,
                artifacts=artifact_objects,
                return_value=report.return_value,
                metrics=metrics_dict,
            )

        elif domain_status == ExecutionStatus.FAILED:
            # Use stderr as the error message, keeping both stdout and stderr
            error_message = report.stderr if report.stderr else "Execution failed"
            execution.mark_failed(
                error_message=error_message,
                exit_code=report.exit_code,
                stdout=report.stdout,
                stderr=report.stderr,
            )

        elif domain_status == ExecutionStatus.TIMEOUT:
            execution.mark_timeout()

        elif domain_status == ExecutionStatus.CRASHED:
            execution.mark_crashed()

        # 6. Persist
        await execution_repo.save(execution)

        # 6.5. Commit, so other requests see the updated execution status at once
        await execution_repo.commit()

        logger.info(
            f"Execution result recorded: {execution_id}, status={domain_status}, "
            f"exit_code={report.exit_code}"
        )

        # 6. Answer 201 for the first report
        return JSONResponse(
            status_code=status.HTTP_201_CREATED,
            content={"message": "Result recorded successfully"},
        )

    except ValueError as e:
        # An invalid status transition, such as marking a never-finished execution complete
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail=message("Sandbox.State.Conflict", error=str(e)),
        )
    except Exception as e:
        logger.error(f"Failed to record execution result: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail=message("Sandbox.Internal.Error", error=str(e)),
        )
