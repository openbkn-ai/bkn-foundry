"""
Internal API request and response schemas

The Pydantic models for the internal API the executor calls.
"""

from pydantic import BaseModel, Field
from typing import Optional, Dict, Any, List


class ExecutionMetrics(BaseModel):
    """Execution performance metrics"""

    duration_ms: float = Field(..., description="Wall-clock duration in milliseconds")
    cpu_time_ms: Optional[float] = Field(None, description="CPU time in milliseconds")
    peak_memory_mb: Optional[float] = Field(None, description="Peak memory in MB")
    io_read_bytes: Optional[int] = Field(None, description="Bytes read")
    io_write_bytes: Optional[int] = Field(None, description="Bytes written")


class ArtifactMetadata(BaseModel):
    """File metadata"""

    path: str = Field(..., description="File path, relative to the workspace")
    size: int = Field(..., description="File size in bytes")
    mime_type: str = Field(..., description="MIME type")
    type: str = Field(..., description="File type: artifact, log, or output")
    checksum: Optional[str] = Field(None, description="SHA256 checksum")


class ExecutionResultReport(BaseModel):
    """
    Execution result report request

    The executor calls this to report a result to the control plane.
    """

    status: str = Field(..., description="Execution status: success, failed, timeout, or crashed")
    stdout: str = Field("", description="Standard output")
    stderr: str = Field("", description="Standard error")
    exit_code: int = Field(..., description="Process exit code")
    execution_time: float = Field(..., description="Execution duration in seconds")
    return_value: Optional[Any] = Field(None, description="Return value of the handler function")
    metrics: Optional[ExecutionMetrics] = Field(None, description="Performance metrics")
    artifacts: List[str] = Field(default_factory=list, description="Paths of the files that were produced")


class InternalAPIResponse(BaseModel):
    """Standard internal API response"""

    message: str = Field(..., description="Response message")


class ContainerReadyRequest(BaseModel):
    """Container-ready request"""

    container_id: str = Field(..., description="Container id")
    pod_name: Optional[str] = Field(None, description="Pod name, under Kubernetes")
    executor_port: int = Field(8080, description="Executor HTTP API port")
    ready_at: Optional[str] = Field(None, description="Ready timestamp, ISO 8601")
