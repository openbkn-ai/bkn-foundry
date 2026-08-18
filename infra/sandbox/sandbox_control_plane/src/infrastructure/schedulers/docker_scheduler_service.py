"""
Docker scheduling service

Implements the scheduling policy: pick the best node and create the container.
"""

from typing import List, Optional

from src.domain.services.scheduler import (
    IScheduler,
    RuntimeNode,
    ScheduleRequest,
)
from src.domain.repositories.runtime_node_repository import IRuntimeNodeRepository
from src.domain.repositories.template_repository import ITemplateRepository
from src.domain.value_objects.execution_request import ExecutionRequest
from src.infrastructure.container_scheduler.base import (
    IContainerScheduler,
    ContainerConfig,
)
from src.infrastructure.executors import ExecutorClient
from src.infrastructure.logging import get_logger

logger = get_logger(__name__)


class DockerSchedulerService(IScheduler):
    """
    Docker scheduling service

    The scheduling policy:
    1. Prefer a node with template affinity, meaning the image is already cached
    2. Otherwise pick the healthy node with the lowest load

    A container is bound to its session from creation and lives exactly as long as it.
    """

    def __init__(
        self,
        runtime_node_repo: IRuntimeNodeRepository,
        container_scheduler: IContainerScheduler,
        template_repo: ITemplateRepository,
        executor_client: Optional[ExecutorClient] = None,
        executor_port: int = 8080,
        control_plane_url: str = "http://control-plane:8000",
        disable_bwrap: bool = False,
    ):
        self._runtime_node_repo = runtime_node_repo
        self._container_scheduler = container_scheduler
        self._template_repo = template_repo
        self._executor_client = executor_client or ExecutorClient()
        self._executor_port = executor_port
        self._control_plane_url = control_plane_url
        self._disable_bwrap = disable_bwrap

    async def schedule(self, request: ScheduleRequest) -> RuntimeNode:
        """
        Schedule the session onto the best node

        The policy:
        1. Look for a node that already cached this template, which is template affinity
        2. Otherwise pick the healthy node with the lowest load
        """
        logger.info(
            "Starting node selection",
            session_id=request.session_id,
            template_id=request.template_id,
            cpu_limit=request.resource_limit.cpu,
            memory_limit=request.resource_limit.memory,
        )

        # 1. Read every healthy node
        healthy_nodes = await self.get_healthy_nodes()
        logger.debug(
            "Found healthy nodes",
            node_count=len(healthy_nodes),
            nodes=[n.id for n in healthy_nodes],
        )

        if not healthy_nodes:
            logger.error("No healthy runtime nodes available")
            raise RuntimeError("No healthy runtime nodes available")

        # 2. Sort by template affinity
        affinity_nodes = [node for node in healthy_nodes if node.has_template(request.template_id)]

        logger.debug(
            "Node affinity check",
            template_id=request.template_id,
            affinity_nodes_count=len(affinity_nodes),
            affinity_node_ids=[n.id for n in affinity_nodes],
        )

        if affinity_nodes:
            # Among the affine nodes, take the least loaded
            selected = self._select_least_loaded(affinity_nodes)
            logger.info(
                "Selected affinity node",
                node_id=selected.id,
                session_id=request.session_id,
                reason="template_cached",
                node_load=selected.get_load_ratio(),
                node_sessions=selected.session_count,
            )
            return selected

        # 3. Fall back to load balancing
        selected = self._select_least_loaded(healthy_nodes)
        logger.info(
            "Selected node by load balancing",
            node_id=selected.id,
            session_id=request.session_id,
            reason="load_balancing",
            node_load=selected.get_load_ratio(),
            node_sessions=selected.session_count,
        )
        return selected

    async def get_node(self, node_id: str) -> Optional[RuntimeNode]:
        """Get one node"""
        node_model = await self._runtime_node_repo.find_by_id(node_id)
        if not node_model:
            return None
        return node_model.to_runtime_node()

    async def get_healthy_nodes(self) -> List[RuntimeNode]:
        """Get every healthy node"""
        nodes = await self._runtime_node_repo.find_by_status("online")
        return [node.to_runtime_node() for node in nodes]

    async def mark_node_unhealthy(self, node_id: str) -> None:
        """Mark a node unhealthy"""
        await self._runtime_node_repo.update_status(node_id, "offline")
        logger.warning("Marked node as unhealthy", node_id=node_id)

    def _select_least_loaded(self, nodes: List[RuntimeNode]) -> RuntimeNode:
        """
        Pick the least loaded node from a list

        How it chooses:
        1. Lowest load ratio
        2. On a tie, the fewest sessions
        """
        return min(nodes, key=lambda n: (n.get_load_ratio(), n.session_count))

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
        Create the container for a session, synchronously

        The container is bound to the session from creation.

        Args:
            session_id: session id
            template_id: template id
            image: container image
            resource_limit: resource limits
            env_vars: environment variables
            workspace_path: workspace path
            node_id: target node id
            dependencies: Python dependencies as pip specifiers

        Returns:
            The container id, which is the container name
        """
        import json

        logger.info(
            "Creating container for session",
            session_id=session_id,
            template_id=template_id,
            image=image,
            node_id=node_id,
            dependencies_count=len(dependencies) if dependencies else 0,
            dependencies=dependencies,
        )

        # Read the node
        node = await self.get_node(node_id)
        if not node:
            logger.error("Node not found", node_id=node_id, session_id=session_id)
            raise RuntimeError(f"Node not found: {node_id}")

        logger.debug(
            "Node retrieved",
            node_id=node.id,
            node_type=node.type,
            node_status=node.status,
        )

        # Build the container configuration.
        # dependencies_json goes to docker_scheduler.py, which generates the entrypoint script from it.
        dependencies_json = json.dumps(dependencies) if dependencies else ""

        container_name = f"sandbox-{session_id}"

        config = ContainerConfig(
            image=image,
            name=container_name,
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
                "dependencies": dependencies_json,  # passed through to docker_scheduler.py
            },
        )

        logger.debug(
            "Container configuration prepared",
            session_id=session_id,
            container_name=container_name,
            cpu_limit=config.cpu_limit,
            memory_limit=config.memory_limit,
            disk_limit=config.disk_limit,
            workspace_path=workspace_path,
            env_vars_count=len(config.env_vars),
        )

        # Create the container synchronously and wait for it
        try:
            logger.info(
                "Creating container",
                session_id=session_id,
                container_name=container_name,
                image=image,
            )

            container_id = await self._container_scheduler.create_container(config)

            logger.info(
                "Container created, starting now",
                session_id=session_id,
                container_id=container_id,
                container_name=container_name,
            )

            await self._container_scheduler.start_container(container_id)

            logger.info(
                "Container started successfully",
                session_id=session_id,
                container_id=container_id,
                container_name=container_name,
                node_id=node.id,
            )

            # Read the container status to confirm
            try:
                container_info = await self._container_scheduler.get_container_status(
                    container_name
                )
                logger.info(
                    "Container status after start",
                    session_id=session_id,
                    container_id=container_id,
                    container_name=container_name,
                    status=container_info.status,
                    ip_address=container_info.ip_address,
                )
            except Exception as status_error:
                logger.warning(
                    "Failed to get container status after start",
                    session_id=session_id,
                    container_id=container_id,
                    error=str(status_error),
                )

            # Use the container name as the id, for executor traffic
            return container_name

        except Exception as e:
            logger.exception(
                "Failed to create/start container",
                session_id=session_id,
                container_name=container_name,
                error=str(e),
                error_type=type(e).__name__,
            )
            raise

    async def destroy_container(self, container_id: str, timeout: int = 10) -> None:
        """
        Destroy the container

        A container is always destroyed; nothing is released back into a warm pool.
        """
        try:
            await self._container_scheduler.stop_container(container_id, timeout=timeout)
            await self._container_scheduler.remove_container(container_id)
            logger.info("Destroyed container", container_id=container_id)
        except Exception as e:
            logger.error(
                "Failed to destroy container",
                container_id=container_id,
                error=str(e),
            )
            raise

    async def get_container_info(self, container_id: str):
        """Get the container information"""
        return await self._container_scheduler.get_container_status(container_id)

    async def execute(
        self,
        session_id: str,
        container_id: str,
        execution_request: ExecutionRequest,
    ) -> str:
        """
        Submit an execution request to the executor inside the container

        Talks over HTTP to the sandbox-executor running in the container.

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
        # Read the container information to build the executor URL
        container_info = await self._container_scheduler.get_container_status(container_id)

        # Build the executor URL.
        # The container name addresses it inside the Docker network,
        # in the form sandbox-{session_id}.
        container_name = container_info.name
        executor_url = self._build_executor_url(container_name)

        logger.info(
            "Submitting execution to executor",
            executor_url=executor_url,
            session_id=session_id,
            container_id=container_id,
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
                "Execution submitted successfully",
                execution_id=execution_id,
                session_id=session_id,
            )

            return execution_id

        except Exception as e:
            logger.error(
                "Failed to submit execution to executor",
                executor_url=executor_url,
                error=str(e),
            )
            raise

    async def get_executor_url(self, container_id: str) -> str:
        """Resolve the executor URL from a container id."""
        return self._build_executor_url(container_id)

    def _build_executor_url(self, container_name: str) -> str:
        return f"http://{container_name}:{self._executor_port}"
