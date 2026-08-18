"""
Execution data transfer objects

Carries data between the application layer and the interface layer.
"""

from dataclasses import dataclass
from datetime import datetime
from typing import Optional, List

from src.domain.value_objects.artifact import Artifact


@dataclass
class ArtifactDTO:
    """File artifact data transfer object"""

    path: str
    size: int
    mime_type: str
    type: str
    created_at: datetime
    checksum: Optional[str] = None

    @classmethod
    def from_entity(cls, artifact: Artifact) -> "ArtifactDTO":
        """Build the DTO from the domain entity"""
        return cls(
            path=artifact.path,
            size=artifact.size,
            mime_type=artifact.mime_type,
            type=artifact.type.value,
            created_at=artifact.created_at,
            checksum=artifact.checksum,
        )


@dataclass
class ExecutionDTO:
    """Execution data transfer object"""

    id: str
    session_id: str
    code: str
    language: str
    timeout: int  # timeout in seconds
    status: str
    exit_code: Optional[int] = None
    error_message: Optional[str] = None
    execution_time: Optional[float] = None
    stdout: str = ""
    stderr: str = ""
    artifacts: List[ArtifactDTO] = None
    retry_count: int = 0
    created_at: datetime = None
    started_at: Optional[datetime] = None
    completed_at: Optional[datetime] = None
    last_heartbeat_at: Optional[datetime] = None
    return_value: Optional[dict] = None  # handler return value
    metrics: Optional[dict] = None  # performance metrics

    def __post_init__(self):
        """Apply the defaults"""
        if self.artifacts is None:
            self.artifacts = []
        if self.created_at is None:
            self.created_at = datetime.now()

    @classmethod
    def from_entity(cls, execution) -> "ExecutionDTO":
        """Build the DTO from the domain entity"""
        return cls(
            id=execution.id,
            session_id=execution.session_id,
            code=execution.code,
            language=execution.language,
            timeout=execution.timeout,
            status=execution.state.status.value,
            exit_code=execution.state.exit_code,
            error_message=execution.state.error_message,
            execution_time=execution.execution_time,
            stdout=execution.stdout,
            stderr=execution.stderr,
            artifacts=[ArtifactDTO.from_entity(artifact) for artifact in execution.artifacts],
            retry_count=execution.retry_count,
            created_at=execution.created_at,
            started_at=None,  # Not tracked in domain entity
            completed_at=execution.completed_at,
            last_heartbeat_at=execution.last_heartbeat_at,
            return_value=execution.return_value,
            metrics=execution.metrics,
        )
