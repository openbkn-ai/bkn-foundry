"""
Execution status value objects

Every state a session or an execution can be in.
"""

from enum import Enum
from dataclasses import dataclass


class SessionStatus(str, Enum):
    """Session status"""

    CREATING = "creating"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    TIMEOUT = "timeout"
    TERMINATED = "terminated"


class ExecutionStatus(str, Enum):
    """Execution status"""

    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    TIMEOUT = "timeout"
    CRASHED = "crashed"


@dataclass(frozen=True)
class ExecutionState:
    """Execution status value object, immutable"""

    status: ExecutionStatus
    exit_code: int | None = None
    error_message: str | None = None

    def is_terminal(self) -> bool:
        """Whether it is terminal and can no longer change"""
        return self.status in {
            ExecutionStatus.COMPLETED,
            ExecutionStatus.FAILED,
            ExecutionStatus.TIMEOUT,
        }

    def can_retry(self) -> bool:
        """Whether it may be retried"""
        return self.status == ExecutionStatus.CRASHED
