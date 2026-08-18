"""
Kubernetes scheduling service

Implements the scheduling policy, creating Pods through the Kubernetes API.
"""

import logging
import os
from typing import List, Optional

from src.domain.services.scheduler import (
    IScheduler,
    RuntimeNode,
    ScheduleRequest,
)
from src.domain.repositories.template_repository import ITemplateRepository
from src.domain.value_objects.execution_request import ExecutionRequest
from src.infrastructure.container_scheduler.base import (
    IContainerScheduler,
    ContainerConfig,
    ControlPlaneOwnerContext,
)
from src.infrastructure.executors import ExecutorClient

logger = logging.getLogger(__name__)


class K8sSchedulerService(IScheduler):
    """
    Kubernetes scheduling service

    Creates and manages Pods through the Kubernetes API:
    1. No node selection of its own; the K8s scheduler handles that
    2. Creates a Pod rather than a container
    3. The Pod lives exactly as long as the session
    """

    def __init__(
        self,
        container_scheduler: IContainerScheduler,
        template_repo: ITemplateRepository,
        executor_client: Optional[ExecutorClient] = None,
        executor_port: int = 8080,
        control_plane_url: str = "http://sandbox-control-plane.sandbox-system.svc.cluster.local:8000",
        disable_bwrap: bool = True,  # bwrap is off by default under K8s
    ):
        self._container_scheduler = container_scheduler
        self._template_repo = template_repo
        self._executor_client = executor_client or ExecutorClient()
        self._executor_port = executor_port
        self._control_plane_url = control_plane_url
        self._disable_bwrap = disable_bwrap
        self._owner_context = self._load_owner_context()

        # The K8s cluster counts as one logical node
        self._cluster_node = RuntimeNode(
            id="k8s-cluster",
            type="kubernetes",
            url="kubernetes://cluster",
            status="healthy",
            cpu_usage=0.0,
            mem_usage=0.0,
            session_count=0,
            max_sessions=1000,
            cached_templates=[],
        )

    def _load_owner_context(self) -> ControlPlaneOwnerContext:
        """Load the owner context of the current control plane Pod."""
        pod_name = os.getenv("POD_NAME", "").strip()
        pod_uid = os.getenv("POD_UID", "").strip()
        if not pod_name or not pod_uid:
            raise RuntimeError(
                "POD_NAME and POD_UID must be set in Kubernetes mode before creating executor pods"
            )

        return ControlPlaneOwnerContext(
            pod_name=pod_name,
            pod_uid=pod_uid,
        )

    async def schedule(self, request: ScheduleRequest) -> RuntimeNode:
        """
        Schedule the session onto the K8s cluster

        Under K8s the scheduling decision belongs to the Kubernetes scheduler,
        so this returns a single virtual node standing for the cluster.
        """
        logger.info(f"Scheduling session to K8s cluster: template={request.template_id}")
        return self._cluster_node

    async def get_node(self, node_id: str) -> Optional[RuntimeNode]:
        """Get one node"""
        if node_id == "k8s-cluster":
            return self._cluster_node
        return None

    async def get_healthy_nodes(self) -> List[RuntimeNode]:
        """Get every healthy node"""
        return [self._cluster_node]

    async def mark_node_unhealthy(self, node_id: str) -> None:
        """Mark a node unhealthy"""
        # Not needed under K8s
        logger.warning(f"mark_node_unhealthy called in K8s environment: {node_id}")

    async def create_container_for_session(
        self,
        session_id: str,
        template_id: str,
        image: str,
        resource_limit,
        env_vars: dict,
        workspace_path: str,
        node_id: str,
        dependencies: list = None,
    ) -> str:
        """
        Create the Pod for a session

        Args:
            session_id: session id
            template_id: template id
            image: container image
            resource_limit: resource limits
            env_vars: environment variables
            workspace_path: workspace path
            node_id: target node id, ignored under K8s
            dependencies: Python dependencies

        Returns:
            The Pod name
        """
        import json

        # Read the template
        template = await self._template_repo.find_by_id(template_id)
        if not template:
            raise RuntimeError(f"Template not found: {template_id}")

        # Build the container configuration
        dependencies_json = json.dumps(dependencies) if dependencies else ""

        # Debug: log the CONTROL_PLANE_URL in use
        logger.info(f"Creating executor pod with CONTROL_PLANE_URL: {self._control_plane_url}")

        config = ContainerConfig(
            image=image,
            name=f"sandbox-{session_id}",
            env_vars={
                **env_vars,
                "SESSION_ID": session_id,
                "WORKSPACE_PATH": workspace_path,
                "CONTROL_PLANE_URL": self._control_plane_url,
                "DISABLE_BWRAP": "true" if self._disable_bwrap else "false",
            },
            cpu_limit=resource_limit.cpu,
            memory_limit=resource_limit.memory,
            disk_limit=resource_limit.disk,
            workspace_path=workspace_path,
            labels={
                "session_id": session_id,
                "template_id": template_id,
                "managed_by": "sandbox-control-plane",
                "dependencies": dependencies_json,
            },
            owner_context=self._owner_context,
        )

        # Create the Pod
        try:
            pod_name = await self._container_scheduler.create_container(config)
            logger.info(f"Created Pod {pod_name} for session {session_id}")
            return pod_name

        except Exception as e:
            logger.error(f"Failed to create Pod for session {session_id}: {e}")
            raise

    async def destroy_container(self, container_id: str, timeout: int = 10) -> None:
        """
        Destroy the Pod
        """
        try:
            await self._container_scheduler.stop_container(container_id, timeout=timeout)
            await self._container_scheduler.remove_container(container_id)
            logger.info(f"Destroyed Pod {container_id}")
        except Exception as e:
            logger.error(f"Failed to destroy Pod {container_id}: {e}")
            raise

    async def get_container_info(self, container_id: str):
        """Get the Pod information"""
        return await self._container_scheduler.get_container_status(container_id)

    async def execute(
        self,
        session_id: str,
        container_id: str,
        execution_request: ExecutionRequest,
    ) -> str:
        """
        Submit an execution request to the executor inside the Pod

        Args:
            session_id: session id
            container_id: Pod name
            execution_request: the execution request

        Returns:
            execution_id: execution task id
        """
        # Read the Pod IP from the K8s API
        executor_url = await self.get_executor_url(container_id)
        logger.info(
            f"Submitting execution to executor: {executor_url}, session_id={session_id}, pod_name={container_id}"
        )

        # Submit through the executor client
        try:
            execution_id = await self._executor_client.submit_execution(
                executor_url=executor_url,
                execution_id=execution_request.execution_id or "",
                session_id=session_id,
                code=execution_request.code,
                language=execution_request.language,
                event=execution_request.event,
                timeout=execution_request.timeout,
                env_vars=execution_request.env_vars,
                working_directory=execution_request.working_directory,
            )

            logger.info(
                f"Execution submitted successfully: execution_id={execution_id}, session_id={session_id}"
            )

            return execution_id

        except Exception as e:
            logger.error(f"Failed to submit execution to executor: {executor_url}, error={e}")
            raise

    async def get_executor_url(self, container_id: str) -> str:
        """Resolve the executor URL from a Pod name."""
        import asyncio

        pod_name = container_id
        namespace = self._container_scheduler._namespace

        try:
            pod_info = await asyncio.to_thread(
                self._container_scheduler._core_v1.read_namespaced_pod,
                name=pod_name,
                namespace=namespace,
            )
            pod_ip = pod_info.status.pod_ip
            if not pod_ip:
                raise RuntimeError(f"Pod {pod_name} does not have an IP address yet")

            return f"http://{pod_ip}:{self._executor_port}"
        except Exception as e:
            logger.error(f"Failed to get pod IP for {pod_name}: {e}")
            raise
