"""
Runtime node repository interface

The port for runtime node persistence.
"""

from abc import ABC, abstractmethod
from typing import List, Optional


class IRuntimeNodeRepository(ABC):
    """
    Runtime node repository interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    """

    @abstractmethod
    async def save(self, node) -> None:
        """Save the node, creating or updating it"""
        pass

    @abstractmethod
    async def find_by_id(self, node_id: str) -> Optional:
        """Find a node by id"""
        pass

    @abstractmethod
    async def find_by_hostname(self, hostname: str) -> Optional:
        """Find a node by hostname"""
        pass

    @abstractmethod
    async def find_by_status(self, status: str) -> List:
        """Find nodes by status"""
        pass

    @abstractmethod
    async def find_all(self, offset: int = 0, limit: int = 100) -> List:
        """Find every node"""
        pass

    @abstractmethod
    async def update_status(self, node_id: str, status: str) -> None:
        """Update the node status"""
        pass

    @abstractmethod
    async def update_heartbeat(self, node_id: str) -> None:
        """Update the node heartbeat time"""
        pass

    @abstractmethod
    async def allocate_resources(self, node_id: str, cpu_cores: float, memory_mb: int) -> None:
        """Allocate resources"""
        pass

    @abstractmethod
    async def release_resources(self, node_id: str, cpu_cores: float, memory_mb: int) -> None:
        """Release resources"""
        pass

    @abstractmethod
    async def increment_container_count(self, node_id: str) -> None:
        """Increment the container count"""
        pass

    @abstractmethod
    async def decrement_container_count(self, node_id: str) -> None:
        """Decrement the container count"""
        pass
