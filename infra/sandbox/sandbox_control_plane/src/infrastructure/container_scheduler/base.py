"""
Container scheduler interface

The abstraction over container operations.
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Optional, Dict, Any


@dataclass(frozen=True)
class ControlPlaneOwnerContext:
    """The owner context of the current control plane Pod."""

    pod_name: str
    pod_uid: str


@dataclass(frozen=True)
class ContainerOwnershipInfo:
    """Who a container or Pod currently belongs to."""

    owner_pod_name: Optional[str]
    owner_pod_uid: Optional[str]
    annotations: Dict[str, str]
    has_owner_reference: bool


@dataclass
class ContainerConfig:
    """Container configuration"""

    image: str
    name: str
    env_vars: Dict[str, str]
    cpu_limit: str  # such as "1" or "2"
    memory_limit: str  # such as "512Mi" or "1Gi"
    disk_limit: str  # such as "1Gi" or "10Gi"
    workspace_path: str  # S3 path, such as "s3://bucket/sessions/{session_id}/"
    labels: Dict[str, str]
    network_name: str = "sandbox_network"  # Docker network name
    owner_context: Optional[ControlPlaneOwnerContext] = None


@dataclass
class ContainerInfo:
    """Container information"""

    id: str
    name: str
    image: str
    status: str  # created, running, paused, exited, deleting
    ip_address: Optional[str]
    created_at: str
    started_at: Optional[str]
    exited_at: Optional[str]
    exit_code: Optional[int]


@dataclass
class ContainerResult:
    """Container execution result"""

    status: str
    stdout: str
    stderr: str
    exit_code: int


class IContainerScheduler(ABC):
    """
    Container scheduler interface

    Defines the container lifecycle operations.
    """

    @abstractmethod
    async def create_container(self, config: ContainerConfig) -> str:
        """
        Create the container

        Returns the container id
        """
        pass

    @abstractmethod
    async def start_container(self, container_id: str) -> None:
        """Start the container"""
        pass

    @abstractmethod
    async def stop_container(self, container_id: str, timeout: int = 10) -> None:
        """Stop the container"""
        pass

    @abstractmethod
    async def remove_container(self, container_id: str, force: bool = True) -> None:
        """Delete the container"""
        pass

    @abstractmethod
    async def get_container_status(self, container_id: str) -> ContainerInfo:
        """Get the container status"""
        pass

    @abstractmethod
    async def is_container_running(self, container_id: str) -> bool:
        """
        Check whether the container is running

        Queries the Docker API directly, without going through the database.
        StateSyncService uses this.

        Args:
            container_id: container id

        Returns:
            bool: whether the container is running
        """
        pass

    @abstractmethod
    async def get_container_logs(
        self, container_id: str, tail: int = 100, since: Optional[str] = None
    ) -> str:
        """Get the container logs"""
        pass

    @abstractmethod
    async def wait_container(
        self, container_id: str, timeout: Optional[int] = None
    ) -> ContainerResult:
        """Wait for the container to finish"""
        pass

    @abstractmethod
    async def ping(self) -> bool:
        """Check the scheduler connection"""
        pass

    async def get_container_ownership(
        self,
        container_id: str,
    ) -> Optional[ContainerOwnershipInfo]:
        """
        Get who the container belongs to.

        Returns None by default, so a scheduler without an owner concept can reuse this.
        """
        return None
