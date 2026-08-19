"""Unit tests for session."""
import pytest
from datetime import datetime, timedelta
from zoneinfo import ZoneInfo

from src.domain.entities.session import Session
from src.domain.value_objects.resource_limit import ResourceLimit
from src.domain.value_objects.execution_status import SessionStatus


class TestSession:
    """Tests for TestSession."""

    def test_create_session(self):
        """Test create session."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        assert session.id == "sess_20240115_abc123"
        assert session.template_id == "python-datascience"
        assert session.status == SessionStatus.CREATING
        assert session.is_active() is True

    def test_mark_as_running(self):
        """Test mark as running."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        session.mark_as_running(
            runtime_node="node-1",
            container_id="container-123"
        )

        assert session.status == SessionStatus.RUNNING
        assert session.runtime_node == "node-1"
        assert session.container_id == "container-123"

    def test_mark_as_running_invalid_transition(self):
        """Test mark as running invalid transition."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        with pytest.raises(ValueError, match="Cannot mark session as running"):
            session.mark_as_running("node-1", "container-123")

    def test_mark_as_terminated(self):
        """Test mark as terminated."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        session.mark_as_terminated()

        assert session.is_terminated() is True
        assert session.is_active() is False

    def test_is_idle(self):
        """Test is idle."""
        # Create a session that has been inactive for more than 30 minutes.
        old_time = datetime.now() - timedelta(minutes=35)

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
            last_activity_at=old_time,
        )

        assert session.is_idle(threshold_minutes=30) is True

    def test_should_cleanup(self):
        """Test should cleanup."""
        # Idle session.
        old_time = datetime.now() - timedelta(minutes=35)

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
            last_activity_at=old_time,
        )

        assert session.should_cleanup() is True

    def test_add_execution(self):
        """Test add execution."""
        from src.domain.entities.execution import Execution
        from src.domain.value_objects.execution_status import ExecutionStatus, ExecutionState

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        execution = Execution(
            id="exec_20240115_xyz789",
            session_id=session.id,
            code="print('hello')",
            language="python",
            state=ExecutionState(status=ExecutionStatus.PENDING),
        )

        session.add_execution(execution)

        assert len(session.get_executions()) == 1
        assert session.get_executions()[0].id == "exec_20240115_xyz789"

    def test_add_execution_wrong_session(self):
        """Test add execution wrong session."""
        from src.domain.entities.execution import Execution
        from src.domain.value_objects.execution_status import ExecutionStatus, ExecutionState

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        execution = Execution(
            id="exec_20240115_xyz789",
            session_id="different_session_id",  # Wrong session ID.
            code="print('hello')",
            language="python",
            state=ExecutionState(status=ExecutionStatus.PENDING),
        )

        with pytest.raises(ValueError, match="does not belong to this session"):
            session.add_execution(execution)

    def test_invalid_timeout(self):
        """Test invalid timeout."""
        with pytest.raises(ValueError, match="timeout must be positive"):
            Session(
                id="sess_20240115_abc123",
                template_id="python-datascience",
                status=SessionStatus.CREATING,
                resource_limit=ResourceLimit.default(),
                workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
                runtime_type="docker",
                timeout=-1,  # Invalid value.
            )

    def test_invalid_workspace_path(self):
        """Test invalid workspace path."""
        with pytest.raises(ValueError, match="workspace_path cannot be empty"):
            Session(
                id="sess_20240115_abc123",
                template_id="python-datascience",
                status=SessionStatus.CREATING,
                resource_limit=ResourceLimit.default(),
                workspace_path="",  # Invalid value.
                runtime_type="docker",
            )

    def test_mark_as_completed(self):
        """Test mark as completed."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        session.mark_as_completed()

        assert session.status == SessionStatus.COMPLETED
        assert session.completed_at is not None

    def test_mark_as_completed_invalid_transition(self):
        """Test mark as completed invalid transition."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        with pytest.raises(ValueError, match="Cannot mark session as completed"):
            session.mark_as_completed()

    def test_mark_as_failed(self):
        """Test mark as failed."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        session.mark_as_failed()

        assert session.status == SessionStatus.FAILED
        assert session.completed_at is not None

    def test_mark_as_failed_from_creating(self):
        """Test mark as failed from creating."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        session.mark_as_failed()

        assert session.status == SessionStatus.FAILED

    def test_mark_as_failed_invalid_transition(self):
        """Test mark as failed invalid transition."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.TERMINATED,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        with pytest.raises(ValueError, match="Cannot mark session as failed"):
            session.mark_as_failed()

    def test_mark_as_terminated_idempotent(self):
        """Test mark as terminated idempotent."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.TERMINATED,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        # Verify expected behavior.
        session.mark_as_terminated()

        assert session.status == SessionStatus.TERMINATED

    def test_is_expired(self):
        """Test is expired."""
        old_time = datetime.now() - timedelta(hours=7)

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
            created_at=old_time,
        )

        assert session.is_expired(max_hours=6) is True

    def test_is_not_expired(self):
        """Test is not expired."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        assert session.is_expired(max_hours=6) is False

    def test_is_idle_not_active(self):
        """Test is idle not active."""
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.TERMINATED,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        assert session.is_idle() is False

    def test_update_last_activity(self):
        """Test update last activity."""
        old_time = datetime.now() - timedelta(minutes=10)
        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
            last_activity_at=old_time,
        )

        session.update_last_activity()

        assert session.last_activity_at > old_time

    def test_get_running_executions(self):
        """Test get running executions."""
        from src.domain.entities.execution import Execution
        from src.domain.value_objects.execution_status import ExecutionStatus, ExecutionState

        session = Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

        running_execution = Execution(
            id="exec_1",
            session_id=session.id,
            code="print('hello')",
            language="python",
            state=ExecutionState(status=ExecutionStatus.RUNNING),
        )

        completed_execution = Execution(
            id="exec_2",
            session_id=session.id,
            code="print('world')",
            language="python",
            state=ExecutionState(status=ExecutionStatus.COMPLETED),
        )

        session.add_execution(running_execution)
        session.add_execution(completed_execution)

        running = session.get_running_executions()

        assert len(running) == 1
        assert running[0].id == "exec_1"


class TestInstalledDependency:
    """Tests for TestInstalledDependency."""

    def test_create_installed_dependency(self):
        """Test create installed dependency."""
        from src.domain.entities.session import InstalledDependency

        dep = InstalledDependency(
            name="requests",
            version="2.31.0",
            install_location="/workspace/.venv/",
            install_time=datetime.now(),
            is_from_template=False
        )

        assert dep.name == "requests"
        assert dep.version == "2.31.0"
        assert dep.install_location == "/workspace/.venv/"
        assert dep.is_from_template is False


class TestSessionDependencies:
    """Tests for TestSessionDependencies."""

    @pytest.fixture
    def session(self):
        """Create session."""
        return Session(
            id="sess_20240115_abc123",
            template_id="python-datascience",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_20240115_abc123",
            runtime_type="docker",
        )

    def test_set_dependencies_installing(self, session):
        """Test set dependencies installing."""
        session.set_dependencies_installing()

        assert session.dependency_install_status == "installing"

    def test_set_dependencies_completed(self, session):
        """Test set dependencies completed."""
        from src.domain.entities.session import InstalledDependency

        installed = [
            InstalledDependency(
                name="requests",
                version="2.31.0",
                install_location="/opt/sandbox-venv",
                install_time=datetime.now(),
                is_from_template=False
            )
        ]

        session.set_dependencies_completed(installed)

        assert session.dependency_install_status == "completed"
        assert len(session.installed_dependencies) == 1

    def test_set_dependencies_failed(self, session):
        """Test set dependencies failed."""
        session.set_dependencies_failed("pip install failed")

        assert session.dependency_install_status == "failed"
        assert session.dependency_install_error == "pip install failed"

    def test_has_dependencies_true(self, session):
        """Test has dependencies true."""
        session.requested_dependencies = ["requests", "pandas"]

        assert session.has_dependencies() is True

    def test_has_dependencies_false(self, session):
        """Test has dependencies false."""
        assert session.has_dependencies() is False

    def test_is_dependency_install_pending(self, session):
        """Test is dependency install pending."""
        assert session.is_dependency_install_pending() is True  # default "pending"

        session.set_dependencies_installing()
        assert session.is_dependency_install_pending() is True

        session.set_dependencies_completed([])
        assert session.is_dependency_install_pending() is False

    def test_is_dependency_install_successful(self, session):
        """Test is dependency install successful."""
        session.set_dependencies_completed([])

        assert session.is_dependency_install_successful() is True

    def test_is_dependency_install_successful_false(self, session):
        """Test is dependency install successful false."""
        session.set_dependencies_failed("error")

        assert session.is_dependency_install_successful() is False
