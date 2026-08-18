"""
Session repository interface

The port for session persistence.
"""

from abc import ABC, abstractmethod
from typing import List, Optional
from datetime import datetime

from src.domain.entities.session import Session


class ISessionRepository(ABC):
    """
    Session repository interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    """

    @abstractmethod
    async def save(self, session: Session) -> None:
        """Save the session, creating or updating it"""
        pass

    @abstractmethod
    async def find_by_id(self, session_id: str) -> Optional[Session]:
        """Find a session by id"""
        pass

    @abstractmethod
    async def find_by_container_id(self, container_id: str) -> Optional[Session]:
        """Find a session by container id"""
        pass

    @abstractmethod
    async def find_by_status(self, status: str, limit: int = 100) -> List[Session]:
        """Find sessions by status"""
        pass

    @abstractmethod
    async def find_by_template(self, template_id: str) -> List[Session]:
        """Find sessions by template id"""
        pass

    @abstractmethod
    async def find_idle_sessions(self, idle_threshold: datetime) -> List[Session]:
        """Find the idle sessions, for automatic cleanup"""
        pass

    @abstractmethod
    async def find_expired_sessions(self, created_before: datetime) -> List[Session]:
        """Find the expired sessions, for automatic cleanup"""
        pass

    @abstractmethod
    async def delete(self, session_id: str) -> None:
        """Delete the session"""
        pass

    @abstractmethod
    async def exists(self, session_id: str) -> bool:
        """Check whether the session exists"""
        pass

    @abstractmethod
    async def count_by_status(self, status: str) -> int:
        """Count the sessions in a status"""
        pass

    @abstractmethod
    async def count_by_node(self, runtime_node: str) -> int:
        """Count the sessions on a node"""
        pass

    @abstractmethod
    async def find_sessions(
        self,
        status: Optional[str] = None,
        template_id: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> List[Session]:
        """
        Find sessions, with filtering and paging

        Args:
            status: filter by session status, optional
            template_id: filter by template id, optional
            limit: how many to return, 1-200, default 50
            offset: offset, for paging

        Returns:
            The session list
        """
        pass

    @abstractmethod
    async def count_sessions(
        self, status: Optional[str] = None, template_id: Optional[str] = None
    ) -> int:
        """
        Count the sessions, with filtering

        Args:
            status: filter by session status, optional
            template_id: filter by template id, optional

        Returns:
            The total
        """
        pass
