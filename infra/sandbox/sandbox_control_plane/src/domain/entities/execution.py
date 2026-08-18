"""
Execution entity

One code execution; part of the session aggregate.
"""

from dataclasses import dataclass, field
from datetime import datetime

from src.domain.value_objects.execution_status import ExecutionStatus, ExecutionState
from src.domain.value_objects.artifact import Artifact


@dataclass
class Execution:
    """
    Execution entity

    The full lifecycle of a single code execution.
    """

    id: str
    session_id: str
    code: str
    language: str
    state: ExecutionState
    timeout: int = 300  # timeout in seconds
    event_data: dict | None = None  # event payload, the handler function input
    created_at: datetime = field(default_factory=datetime.now)
    completed_at: datetime | None = None
    execution_time: float | None = None  # duration in seconds
    stdout: str = ""
    stderr: str = ""
    artifacts: list[Artifact] = field(default_factory=list)
    retry_count: int = 0
    last_heartbeat_at: datetime | None = None
    # Handler return value and performance metrics
    return_value: dict | None = None  # handler return value, JSON serializable
    metrics: dict | None = None  # performance metrics, a JSON object

    def __post_init__(self):
        """Validate after construction"""
        if not self.code:
            raise ValueError("code cannot be empty")
        if not self.language:
            raise ValueError("language cannot be empty")

    # ============== Domain behaviour ==============

    def mark_running(self) -> None:
        """Mark it running"""
        if self.state.status != ExecutionStatus.PENDING:
            raise ValueError(f"Cannot mark execution as running from status: {self.state.status}")

        self.state = ExecutionState(status=ExecutionStatus.RUNNING)
        self.last_heartbeat_at = datetime.now()

    def mark_completed(
        self,
        stdout: str,
        stderr: str,
        exit_code: int,
        execution_time: float,
        artifacts: list[Artifact] | None = None,
        return_value: dict | None = None,
        metrics: dict | None = None,
    ) -> None:
        """Mark it completed"""
        if self.state.status != ExecutionStatus.RUNNING:
            raise ValueError(f"Cannot mark execution as completed from status: {self.state.status}")

        self.state = ExecutionState(status=ExecutionStatus.COMPLETED, exit_code=exit_code)
        self.stdout = stdout
        self.stderr = stderr
        self.execution_time = execution_time
        self.artifacts = artifacts or []
        self.return_value = return_value  # handler return value
        self.metrics = metrics  # performance metrics
        self.completed_at = datetime.now()
        self.last_heartbeat_at = datetime.now()

    def mark_failed(
        self,
        error_message: str,
        exit_code: int | None = None,
        stdout: str | None = None,
        stderr: str | None = None,
    ) -> None:
        """Mark it failed"""
        self.state = ExecutionState(
            status=ExecutionStatus.FAILED, exit_code=exit_code, error_message=error_message
        )
        self.stdout = stdout or ""
        self.stderr = stderr or ""
        self.completed_at = datetime.now()

    def mark_timeout(self) -> None:
        """Mark it timed out"""
        self.state = ExecutionState(status=ExecutionStatus.TIMEOUT)
        self.completed_at = datetime.now()

    def mark_crashed(self) -> None:
        """Mark it crashed, which is retryable"""
        self.state = ExecutionState(status=ExecutionStatus.CRASHED)

    def update_heartbeat(self) -> None:
        """Update the heartbeat time"""
        self.last_heartbeat_at = datetime.now()

    def increment_retry_count(self) -> None:
        """Increment the retry count"""
        self.retry_count += 1

    # ============== Domain queries ==============

    def is_running(self) -> bool:
        """Whether it is running"""
        return self.state.status == ExecutionStatus.RUNNING

    def is_terminal(self) -> bool:
        """Whether it is terminal"""
        return self.state.is_terminal()

    def can_retry(self, max_retries: int = 3) -> bool:
        """Whether it may be retried"""
        return self.state.can_retry() and self.retry_count < max_retries

    def is_heartbeat_timeout(self, timeout_seconds: int = 15) -> bool:
        """Whether the heartbeat timed out"""
        if not self.last_heartbeat_at or not self.is_running():
            return False
        elapsed = (datetime.now() - self.last_heartbeat_at).total_seconds()
        return elapsed > timeout_seconds
