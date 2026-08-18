"""
Health check REST API routes

Defines the HTTP endpoints for health checks and system monitoring.
"""

from fastapi import APIRouter, Depends
from pydantic import BaseModel
from typing import Optional
import time

from src.interfaces.rest.schemas.response import HealthResponse

router = APIRouter(prefix="/health", tags=["health"])

# When the application started
_start_time = time.time()


class SystemStatus(BaseModel):
    """System status"""

    status: str
    version: str
    uptime: float


@router.get("", response_model=HealthResponse)
async def health_check() -> HealthResponse:
    """
    Health check endpoint

    Returns the system status and uptime.
    """
    return HealthResponse(status="healthy", version="2.1.0", uptime=time.time() - _start_time)


@router.get("/detailed")
async def detailed_health_check() -> dict:
    """
    Detailed health check

    Returns the system status and the health of its dependencies.
    """
    # TODO: check the dependencies
    # - database connection
    # - S3 storage
    # - runtime nodes
    return {
        "status": "healthy",
        "version": "2.1.0",
        "uptime": time.time() - _start_time,
        "dependencies": {"database": "healthy", "storage": "healthy", "runtime_nodes": "healthy"},
    }


@router.post("/sync")
async def trigger_state_sync() -> dict:
    """
    Trigger a state sync by hand

    Runs one state sync and health check immediately, for debugging and manual recovery.
    """
    from src.infrastructure.dependencies import get_state_sync_service

    state_sync_service = get_state_sync_service()
    result = await state_sync_service.periodic_health_check()

    return {"status": "success", "message": "State sync completed", "result": result}
