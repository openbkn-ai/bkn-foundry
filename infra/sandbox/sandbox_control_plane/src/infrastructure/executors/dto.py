"""
Executor API data transfer objects

The request and response models used when talking to the executor HTTP API.
"""

from dataclasses import dataclass
from typing import Dict, Any, Optional
from pydantic import BaseModel, Field


class ExecutorExecuteRequest(BaseModel):
    """
    Executor execution request

    Matches the executor POST /execute endpoint.
    """

    execution_id: str = Field(..., description="Unique execution identifier")
    session_id: str = Field(..., description="Session identifier")
    code: str = Field(..., description="Code to execute")
    language: str = Field(..., description="Programming language (python/javascript/shell)")
    event: Dict[str, Any] = Field(default_factory=dict, description="Event data passed to handler")
    timeout: int = Field(default=300, description="Timeout in seconds", ge=1, le=3600)
    env_vars: Dict[str, str] = Field(default_factory=dict, description="Environment variables")
    working_directory: Optional[str] = Field(
        default=None,
        description="Optional execution directory relative to workspace root",
    )

    class Config:
        json_schema_extra = {
            "examples": [
                {
                    "execution_id": "exec_20240115_test0001",
                    "session_id": "sess_test_001",
                    "code": 'def handler(event):\n    name = event.get("name", "World")\n    return {"message": f"Hello, {name}!"}',
                    "language": "python",
                    "event": {"name": "World"},
                    "timeout": 10,
                    "env_vars": {},
                    "working_directory": "src",
                }
            ]
        }


class ExecutorExecuteResponse(BaseModel):
    """
    Executor execution response

    Matches the executor POST /execute response.
    """

    execution_id: str = Field(..., description="Execution identifier")
    status: str = Field(..., description="Execution status (submitted/completed/failed)")
    message: str = Field(default="", description="Status message")


class ExecutorHealthResponse(BaseModel):
    """
    Executor health check response

    Matches the executor GET /health endpoint.
    """

    status: str = Field(..., description="Health status (healthy/unhealthy)")
    version: str = Field(default="1.0.0", description="Executor version")
    uptime_seconds: Optional[float] = Field(None, description="Time since executor started")
    active_executions: Optional[int] = Field(None, description="Number of active executions")


class ExecutorSyncSessionConfigRequest(BaseModel):
    """Executor dependency sync request."""

    session_id: str = Field(..., description="Session identifier")
    language_runtime: str = Field(..., description="Language runtime type")
    python_package_index_url: str = Field(..., description="Python package index url")
    dependencies: list[str] = Field(default_factory=list, description="Final pip spec list")
    sync_mode: str = Field(..., description="replace or merge")


class ExecutorInstalledDependency(BaseModel):
    """An installed dependency the executor reports."""

    name: str
    version: str
    install_location: str
    install_time: str
    is_from_template: bool = False


class ExecutorSyncSessionConfigResponse(BaseModel):
    """Executor dependency sync response."""

    status: str
    installed_dependencies: list[ExecutorInstalledDependency] = Field(default_factory=list)
    error: str = ""
    started_at: Optional[str] = None
    completed_at: Optional[str] = None


@dataclass
class ExecutorContainerInfo:
    """
    Executor container information

    Used to build the executor URL.
    """

    container_id: str
    container_name: str
    executor_port: int = 8080

    @property
    def executor_url(self) -> str:
        """Get the executor URL"""
        return f"http://{self.container_name}:{self.executor_port}"
