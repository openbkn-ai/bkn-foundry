"""
Scheduler domain service interface

The scheduler abstraction: picking the best runtime node.
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import List, Optional, TYPE_CHECKING

from src.domain.value_objects.resource_limit import ResourceLimit

if TYPE_CHECKING:
    from src.domain.value_objects.execution_request import ExecutionRequest


@dataclass
class RuntimeNode:
    """Runtime node value object"""

    id: str
    type: str  # "docker" or "kubernetes"
    url: str  # node API address
    status: str  # "healthy", "unhealthy", "draining"
    cpu_usage: float  # 0.0 - 1.0
    mem_usage: float  # 0.0 - 1.0
    session_count: int
    max_sessions: int
    cached_templates: List[str]

    def is_healthy(self) -> bool:
        """Whether it is healthy"""
        return self.status == "healthy"

    def get_load_ratio(self) -> float:
        """The load ratio: sessions divided by maximum sessions"""
        return self.session_count / self.max_sessions if self.max_sessions > 0 else 1.0

    def has_template(self, template_id: str) -> bool:
        """Whether it has the template cached"""
        return template_id in self.cached_templates


@dataclass
class ScheduleRequest:
    """Scheduling request"""

    template_id: str
    resource_limit: ResourceLimit
    session_id: str | None = None


class IScheduler(ABC):
    """
    Scheduler interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    """

    @abstractmethod
    async def schedule(self, request: ScheduleRequest) -> RuntimeNode:
        """
        Schedule the session onto the best node

        The policy:
        1. Prefer template affinity, meaning the image is already cached
        2. Otherwise load-balance across the healthy nodes
        """
        pass

    @abstractmethod
    async def get_node(self, node_id: str) -> Optional[RuntimeNode]:
        """Get one node"""
        pass

    @abstractmethod
    async def get_healthy_nodes(self) -> List[RuntimeNode]:
        """Get every healthy node"""
        pass

    @abstractmethod
    async def mark_node_unhealthy(self, node_id: str) -> None:
        """Mark a node unhealthy"""
        pass

    @abstractmethod
    async def execute(
        self,
        session_id: str,
        container_id: str,
        execution_request: "ExecutionRequest",
    ) -> str:
        """
        Submit an execution request to the executor inside the container

        Args:
            session_id: session id
            container_id: container id
            execution_request: the execution request

        Returns:
            execution_id: execution task id

        Raises:
            ConnectionError: the executor is unreachable
            TimeoutError: the executor did not answer in time
        """
        pass
