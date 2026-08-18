"""
Execution repository interface

The port for execution record persistence.
"""

from abc import ABC, abstractmethod
from typing import List, Optional
from datetime import datetime

from src.domain.entities.execution import Execution


class IExecutionRepository(ABC):
    """
    Execution repository interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    """

    @abstractmethod
    async def save(self, execution: Execution) -> None:
        """Save the execution record, creating or updating it"""
        pass

    async def commit(self) -> None:
        """Explicitly commit the transaction (optional - some repos may not implement this)"""
        pass

    @abstractmethod
    async def find_by_id(self, execution_id: str) -> Optional[Execution]:
        """Find an execution record by id"""
        pass

    @abstractmethod
    async def find_by_session_id(self, session_id: str, limit: int = 100) -> List[Execution]:
        """Find the execution records of a session"""
        pass

    @abstractmethod
    async def find_by_status(self, status: str, limit: int = 100) -> List[Execution]:
        """Find execution records by status"""
        pass

    @abstractmethod
    async def find_crashed_executions(self, max_retry_count: int) -> List[Execution]:
        """Find the crashed executions that may be retried"""
        pass

    @abstractmethod
    async def find_heartbeat_timeouts(self, timeout_threshold: datetime) -> List[Execution]:
        """Find the executions whose heartbeat timed out"""
        pass

    @abstractmethod
    async def delete(self, execution_id: str) -> None:
        """Delete the execution record"""
        pass

    @abstractmethod
    async def delete_by_session_id(self, session_id: str) -> None:
        """Delete every execution record of a session"""
        pass

    @abstractmethod
    async def count_by_status(self, status: str) -> int:
        """Count the executions in a status"""
        pass
