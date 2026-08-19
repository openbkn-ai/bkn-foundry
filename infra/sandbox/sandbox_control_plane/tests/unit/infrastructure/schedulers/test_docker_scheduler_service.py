"""Unit tests for docker scheduler service."""
import pytest
from unittest.mock import Mock, AsyncMock, patch

from src.infrastructure.schedulers.docker_scheduler_service import DockerSchedulerService
from src.domain.services.scheduler import RuntimeNode, ScheduleRequest
from src.domain.value_objects.resource_limit import ResourceLimit
from tests.helpers import create_mock_template, create_mock_runtime_node


class TestDockerSchedulerService:
    """Tests for TestDockerSchedulerService."""

    @pytest.fixture
    def runtime_node_repo(self):
        """Create runtime node repo."""
        repo = Mock()
        repo.find_by_id = AsyncMock()
        repo.find_by_status = AsyncMock(return_value=[])
        repo.update_status = AsyncMock()
        return repo

    @pytest.fixture
    def container_scheduler(self):
        """Create container scheduler."""
        scheduler = Mock()
        scheduler.create_container = AsyncMock(return_value="container-123")
        scheduler.start_container = AsyncMock()
        scheduler.stop_container = AsyncMock()
        scheduler.remove_container = AsyncMock()
        scheduler.get_container_status = AsyncMock()
        return scheduler

    @pytest.fixture
    def template_repo(self):
        """Create template repo."""
        repo = Mock()
        repo.find_by_id = AsyncMock()
        return repo

    @pytest.fixture
    def executor_client(self):
        """Create executor client."""
        client = Mock()
        client.submit_execution = AsyncMock(return_value="exec-123")
        client.health_check = AsyncMock()
        return client

    @pytest.fixture
    def service(self, runtime_node_repo, container_scheduler, template_repo, executor_client):
        """Create service."""
        return DockerSchedulerService(
            runtime_node_repo=runtime_node_repo,
            container_scheduler=container_scheduler,
            template_repo=template_repo,
            executor_client=executor_client,
            executor_port=8080,
            control_plane_url="http://control-plane:8000",
            disable_bwrap=True,
        )

    @pytest.fixture
    def healthy_node(self):
        """Create healthy node."""
        return create_mock_runtime_node(node_id="node-1", node_type="docker")

    @pytest.fixture
    def schedule_request(self):
        """Create schedule request."""
        return ScheduleRequest(
            session_id="sess-123",
            template_id="python-test",
            resource_limit=ResourceLimit.default(),
        )

    @pytest.mark.asyncio
    async def test_schedule_with_affinity(
        self, service, runtime_node_repo, healthy_node, schedule_request
    ):
        """Test schedule with affinity."""
        # Node has a template cache.
        healthy_node._cached_templates = ["python-test"]
        runtime_node_repo.find_by_status.return_value = [
            healthy_node.to_runtime_node() if hasattr(healthy_node, 'to_runtime_node') else healthy_node
        ]

        # Mock the repository model
        node_model = Mock()
        node_model.to_runtime_node = Mock(return_value=healthy_node)
        runtime_node_repo.find_by_status.return_value = [node_model]

        # Update healthy_node to have the template
        healthy_node.cached_templates = ["python-test"]

        result = await service.schedule(schedule_request)

        assert result is not None
        runtime_node_repo.find_by_status.assert_called_once_with("online")

    @pytest.mark.asyncio
    async def test_schedule_no_healthy_nodes(self, service, runtime_node_repo, schedule_request):
        """Test schedule no healthy nodes."""
        runtime_node_repo.find_by_status.return_value = []

        with pytest.raises(RuntimeError, match="No healthy runtime nodes available"):
            await service.schedule(schedule_request)

    @pytest.mark.asyncio
    async def test_schedule_load_balanced(
        self, service, runtime_node_repo, schedule_request
    ):
        """Test schedule load balanced."""
        # Create multiple nodes with no template cache.
        node1 = create_mock_runtime_node(node_id="node-1")
        node1.cached_templates = []
        node1.session_count = 10
        node1._cpu_usage = 0.8
        node1._mem_usage = 0.7

        node2 = create_mock_runtime_node(node_id="node-2")
        node2.cached_templates = []
        node2.session_count = 5  # Fewer sessions.
        node2._cpu_usage = 0.3
        node2._mem_usage = 0.4

        node_model1 = Mock()
        node_model1.to_runtime_node = Mock(return_value=node1)
        node_model2 = Mock()
        node_model2.to_runtime_node = Mock(return_value=node2)
        runtime_node_repo.find_by_status.return_value = [node_model1, node_model2]

        result = await service.schedule(schedule_request)

        # Should select the node with lower load.
        assert result is not None

    @pytest.mark.asyncio
    async def test_get_node_found(self, service, runtime_node_repo, healthy_node):
        """Test get node found."""
        node_model = Mock()
        node_model.to_runtime_node = Mock(return_value=healthy_node)
        runtime_node_repo.find_by_id.return_value = node_model

        result = await service.get_node("node-1")

        assert result is not None
        runtime_node_repo.find_by_id.assert_called_once_with("node-1")

    @pytest.mark.asyncio
    async def test_get_node_not_found(self, service, runtime_node_repo):
        """Test get node not found."""
        runtime_node_repo.find_by_id.return_value = None

        result = await service.get_node("non-existent")

        assert result is None

    @pytest.mark.asyncio
    async def test_get_healthy_nodes(self, service, runtime_node_repo, healthy_node):
        """Test get healthy nodes."""
        node_model = Mock()
        node_model.to_runtime_node = Mock(return_value=healthy_node)
        runtime_node_repo.find_by_status.return_value = [node_model]

        result = await service.get_healthy_nodes()

        assert len(result) == 1
        runtime_node_repo.find_by_status.assert_called_once_with("online")

    @pytest.mark.asyncio
    async def test_mark_node_unhealthy(self, service, runtime_node_repo):
        """Test mark node unhealthy."""
        await service.mark_node_unhealthy("node-1")

        runtime_node_repo.update_status.assert_called_once_with("node-1", "offline")

    @pytest.mark.asyncio
    async def test_create_container_for_session_success(
        self, service, runtime_node_repo, container_scheduler, healthy_node
    ):
        """Test create container for session success."""
        node_model = Mock()
        node_model.to_runtime_node = Mock(return_value=healthy_node)
        runtime_node_repo.find_by_id.return_value = node_model

        container_info = Mock()
        container_info.status = "running"
        container_info.ip_address = "172.17.0.2"
        container_scheduler.get_container_status.return_value = container_info

        result = await service.create_container_for_session(
            session_id="sess-123",
            template_id="python-test",
            image="python:3.11",
            resource_limit=ResourceLimit.default(),
            env_vars={"TEST": "value"},
            workspace_path="s3://bucket/sessions/sess-123",
            node_id="node-1",
        )

        assert result == "sandbox-sess-123"
        container_scheduler.create_container.assert_called_once()
        container_scheduler.start_container.assert_called_once()

    @pytest.mark.asyncio
    async def test_create_container_node_not_found(
        self, service, runtime_node_repo
    ):
        """Test create container node not found."""
        runtime_node_repo.find_by_id.return_value = None

        with pytest.raises(RuntimeError, match="Node not found"):
            await service.create_container_for_session(
                session_id="sess-123",
                template_id="python-test",
                image="python:3.11",
                resource_limit=ResourceLimit.default(),
                env_vars={},
                workspace_path="s3://bucket/sessions/sess-123",
                node_id="non-existent",
            )

    @pytest.mark.asyncio
    async def test_create_container_with_dependencies(
        self, service, runtime_node_repo, container_scheduler, healthy_node
    ):
        """Test create container with dependencies."""
        node_model = Mock()
        node_model.to_runtime_node = Mock(return_value=healthy_node)
        runtime_node_repo.find_by_id.return_value = node_model

        container_info = Mock()
        container_info.status = "running"
        container_info.ip_address = "172.17.0.2"
        container_scheduler.get_container_status.return_value = container_info

        result = await service.create_container_for_session(
            session_id="sess-123",
            template_id="python-test",
            image="python:3.11",
            resource_limit=ResourceLimit.default(),
            env_vars={},
            workspace_path="s3://bucket/sessions/sess-123",
            node_id="node-1",
            dependencies=[{"name": "requests", "version": ">=2.28.0"}],
        )

        assert result == "sandbox-sess-123"

    @pytest.mark.asyncio
    async def test_destroy_container_success(self, service, container_scheduler):
        """Test destroy container success."""
        await service.destroy_container("container-123")

        container_scheduler.stop_container.assert_called_once()
        container_scheduler.remove_container.assert_called_once()

    @pytest.mark.asyncio
    async def test_destroy_container_with_error(self, service, container_scheduler):
        """Test destroy container with error."""
        container_scheduler.stop_container.side_effect = RuntimeError("Stop failed")

        with pytest.raises(RuntimeError):
            await service.destroy_container("container-123")

    @pytest.mark.asyncio
    async def test_get_container_info(self, service, container_scheduler):
        """Test get container info."""
        container_info = Mock()
        container_scheduler.get_container_status.return_value = container_info

        result = await service.get_container_info("container-123")

        assert result is container_info
        container_scheduler.get_container_status.assert_called_once_with("container-123")

    @pytest.mark.asyncio
    async def test_execute_success(
        self, service, container_scheduler, executor_client
    ):
        """Test execute success."""
        container_info = Mock()
        container_info.name = "sandbox-sess-123"
        container_scheduler.get_container_status.return_value = container_info

        from src.domain.value_objects.execution_request import ExecutionRequest
        execution_request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={},
            working_directory="src/jobs",
        )

        result = await service.execute(
            session_id="sess-123",
            container_id="container-123",
            execution_request=execution_request,
        )

        assert result == "exec-123"
        executor_client.submit_execution.assert_called_once()
        assert executor_client.submit_execution.call_args.kwargs["working_directory"] == "src/jobs"

    def test_select_least_loaded(self, service):
        """Test select least loaded."""
        node1 = create_mock_runtime_node(node_id="node-1")
        node1.session_count = 10
        node1._cpu_usage = 0.8

        node2 = create_mock_runtime_node(node_id="node-2")
        node2.session_count = 5
        node2._cpu_usage = 0.3

        node3 = create_mock_runtime_node(node_id="node-3")
        node3.session_count = 3
        node3._cpu_usage = 0.2

        result = service._select_least_loaded([node1, node2, node3])

        # Should select node3 with lowest load
        assert result.id == "node-3"
