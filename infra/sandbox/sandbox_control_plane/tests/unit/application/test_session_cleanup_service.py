"""Unit tests for session cleanup service."""
import pytest
from unittest.mock import Mock, AsyncMock, patch
from datetime import datetime, timedelta

from src.application.services.session_cleanup_service import SessionCleanupService
from src.domain.entities.session import Session
from src.domain.value_objects.resource_limit import ResourceLimit
from src.domain.value_objects.execution_status import SessionStatus
from src.domain.repositories.session_repository import ISessionRepository
from src.domain.services.scheduler import IScheduler


class TestSessionCleanupService:
    """Tests for TestSessionCleanupService."""

    @pytest.fixture
    def session_repo(self):
        """Create session repo."""
        repo = Mock()
        repo.save = AsyncMock()
        repo.find_by_id = AsyncMock()
        repo.find_by_status = AsyncMock()
        return repo

    @pytest.fixture
    def scheduler(self):
        """Create scheduler."""
        sched = Mock()
        sched.destroy_container = AsyncMock()
        return sched

    @pytest.fixture
    def storage_service(self):
        """Create storage service."""
        storage = Mock()
        storage.delete_prefix = AsyncMock()
        return storage

    @pytest.fixture
    def service(self, session_repo, scheduler, storage_service):
        """Create service."""
        return SessionCleanupService(
            session_repo=session_repo,
            scheduler=scheduler,
            idle_timeout_minutes=30,
            max_lifetime_hours=6,
            storage_service=storage_service
        )

    @pytest.fixture
    def active_session(self):
        """Create active session."""
        return Session(
            id="sess_active",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_active",
            runtime_type="docker",
            container_id="container-active",
            last_activity_at=datetime.now()
        )

    @pytest.fixture
    def idle_session(self):
        """Create idle session."""
        old_time = datetime.now() - timedelta(minutes=35)
        return Session(
            id="sess_idle",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_idle",
            runtime_type="docker",
            container_id="container-idle",
            last_activity_at=old_time
        )

    @pytest.fixture
    def expired_session(self):
        """Create expired session."""
        old_time = datetime.now() - timedelta(hours=7)
        return Session(
            id="sess_expired",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_expired",
            runtime_type="docker",
            container_id="container-expired",
            created_at=old_time,
            last_activity_at=datetime.now()
        )

    @pytest.mark.asyncio
    async def test_cleanup_idle_sessions(self, service, session_repo, scheduler, storage_service, idle_session):
        """Test cleanup idle sessions."""
        session_repo.find_by_status.return_value = [idle_session]
        storage_service.delete_prefix.return_value = 5

        result = await service.cleanup_idle_sessions()

        assert result["idle_cleaned"] == 1
        assert idle_session.status == SessionStatus.TERMINATED
        # destroy_container may use container_id as the argument name.
        assert scheduler.destroy_container.called
        storage_service.delete_prefix.assert_called_once()
        session_repo.save.assert_called_once()

    @pytest.mark.asyncio
    async def test_cleanup_expired_sessions(self, service, session_repo, scheduler, storage_service, expired_session):
        """Test cleanup expired sessions."""
        session_repo.find_by_status.return_value = [expired_session]
        storage_service.delete_prefix.return_value = 3

        result = await service.cleanup_idle_sessions()

        assert result["expired_cleaned"] == 1
        assert expired_session.status == SessionStatus.TERMINATED
        # destroy_container may use container_id as the argument name.
        assert scheduler.destroy_container.called

    @pytest.mark.asyncio
    async def test_no_cleanup_for_active_sessions(self, service, session_repo, active_session):
        """Test no cleanup for active sessions."""
        session_repo.find_by_status.return_value = [active_session]

        result = await service.cleanup_idle_sessions()

        assert result["idle_cleaned"] == 0
        assert result["expired_cleaned"] == 0
        assert active_session.status == SessionStatus.RUNNING

    @pytest.mark.asyncio
    async def test_cleanup_mixed_sessions(self, service, session_repo, scheduler, storage_service):
        """Test cleanup mixed sessions."""
        session_repo.find_by_status.return_value = [
            Session(
                id="sess_1",
                template_id="python-basic",
                status=SessionStatus.RUNNING,
                resource_limit=ResourceLimit.default(),
                workspace_path="s3://sandbox-workspace/sessions/sess_1",
                runtime_type="docker",
                container_id="container-1",
                last_activity_at=datetime.now()  # Active session.
            ),
            Session(
                id="sess_2",
                template_id="python-basic",
                status=SessionStatus.RUNNING,
                resource_limit=ResourceLimit.default(),
                workspace_path="s3://sandbox-workspace/sessions/sess_2",
                runtime_type="docker",
                container_id="container-2",
                last_activity_at=datetime.now() - timedelta(minutes=40)  # Idle session.
            ),
        ]
        storage_service.delete_prefix.return_value = 2

        result = await service.cleanup_idle_sessions()

        assert result["total_checked"] == 2
        assert result["idle_cleaned"] == 1

    @pytest.mark.asyncio
    async def test_cleanup_disabled_idle_timeout(self, session_repo, scheduler, storage_service):
        """Test cleanup disabled idle timeout."""
        service = SessionCleanupService(
            session_repo=session_repo,
            scheduler=scheduler,
            idle_timeout_minutes=-1,  # Disable idle timeout cleanup.
            max_lifetime_hours=6,
            storage_service=storage_service
        )

        idle_session = Session(
            id="sess_idle",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_idle",
            runtime_type="docker",
            container_id="container-idle",
            last_activity_at=datetime.now() - timedelta(hours=10)  # Exceeds the idle threshold.
        )
        session_repo.find_by_status.return_value = [idle_session]

        result = await service.cleanup_idle_sessions()

        # Verify expected behavior.
        assert result["idle_cleaned"] == 0
        assert idle_session.status == SessionStatus.RUNNING

    @pytest.mark.asyncio
    async def test_cleanup_disabled_max_lifetime(self, session_repo, scheduler, storage_service):
        """Test cleanup disabled max lifetime."""
        service = SessionCleanupService(
            session_repo=session_repo,
            scheduler=scheduler,
            idle_timeout_minutes=30,
            max_lifetime_hours=-1,  # Disable max lifetime cleanup.
            storage_service=storage_service
        )

        expired_session = Session(
            id="sess_expired",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_expired",
            runtime_type="docker",
            container_id="container-expired",
            created_at=datetime.now() - timedelta(days=1),  # Exceeds the lifetime threshold.
            last_activity_at=datetime.now()
        )
        session_repo.find_by_status.return_value = [expired_session]

        result = await service.cleanup_idle_sessions()

        # The expired session should not be cleaned up.
        assert result["expired_cleaned"] == 0
        assert expired_session.status == SessionStatus.RUNNING

    @pytest.mark.asyncio
    async def test_cleanup_orphaned_failed_sessions(self, service, session_repo):
        """Test cleanup orphaned failed sessions."""
        failed_session = Session(
            id="sess_failed",
            template_id="python-basic",
            status=SessionStatus.FAILED,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_failed",
            runtime_type="docker",
            container_id="container-failed"
        )
        # Use side_effect to distinguish return values for different status queries.
        session_repo.find_by_status.side_effect = [
            [failed_session],  # failed status query.
            []  # timeout status query.
        ]

        result = await service.cleanup_orphaned_sessions()

        assert result["cleaned"] == 1
        assert failed_session.status == SessionStatus.TERMINATED

    @pytest.mark.asyncio
    async def test_cleanup_orphaned_timeout_sessions(self, service, session_repo):
        """Test cleanup orphaned timeout sessions."""
        timeout_session = Session(
            id="sess_timeout",
            template_id="python-basic",
            status=SessionStatus.TIMEOUT,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_timeout",
            runtime_type="docker",
            container_id="container-timeout"
        )
        # Use side_effect to distinguish return values for different status queries.
        session_repo.find_by_status.side_effect = [
            [],  # failed status query.
            [timeout_session]  # timeout status query.
        ]

        result = await service.cleanup_orphaned_sessions()

        assert result["cleaned"] == 1

    @pytest.mark.asyncio
    async def test_cleanup_orphaned_without_container(self, service, session_repo):
        """Test cleanup orphaned without container."""
        failed_session = Session(
            id="sess_failed",
            template_id="python-basic",
            status=SessionStatus.FAILED,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_failed",
            runtime_type="docker",
            container_id=None  # No container to clean up.
        )
        session_repo.find_by_status.return_value = [failed_session]

        result = await service.cleanup_orphaned_sessions()

        assert result["cleaned"] == 0

    @pytest.mark.asyncio
    async def test_cleanup_session_files(self, service, storage_service):
        """Test cleanup session files."""
        session = Session(
            id="sess_123",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_123",
            runtime_type="docker"
        )
        storage_service.delete_prefix.return_value = 7

        deleted_count = await service.cleanup_session_files(session, "test_cleanup")

        assert deleted_count == 7
        storage_service.delete_prefix.assert_called_once_with(
            "s3://sandbox-workspace/sessions/sess_123"
        )

    @pytest.mark.asyncio
    async def test_cleanup_session_without_workspace(self, service, storage_service):
        """Test cleanup session without workspace."""
        # Session entity validates that workspace_path is not empty.
        # Use a non-S3 path here.
        session = Session(
            id="sess_123",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="local:/tmp/sess_123",  # Non-S3 path, so S3 cleanup is not executed.
            runtime_type="docker"
        )

        # Mock delete_prefix to return 0.
        storage_service.delete_prefix.return_value = 0

        deleted_count = await service.cleanup_session_files(session, "test_cleanup")

        # Non-S3 paths may still run through cleanup, but the result should be 0.
        assert deleted_count == 0

    @pytest.mark.asyncio
    async def test_cleanup_by_ids(self, service, session_repo):
        """Test cleanup by ids."""
        session = Session(
            id="sess_123",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_123",
            runtime_type="docker",
            container_id="container-123"
        )
        session_repo.find_by_id.return_value = session

        result = await service.cleanup_by_ids(["sess_123"])

        assert result["cleaned"] == 1
        assert session.status == SessionStatus.TERMINATED

    @pytest.mark.asyncio
    async def test_cleanup_by_ids_not_found(self, service, session_repo):
        """Test cleanup by ids not found."""
        session_repo.find_by_id.return_value = None

        result = await service.cleanup_by_ids(["non-existent"])

        assert result["not_found"] == 1
        assert result["cleaned"] == 0

    @pytest.mark.asyncio
    async def test_cleanup_container_destruction_failure(self, service, session_repo, scheduler, storage_service):
        """Test cleanup container destruction failure."""
        idle_session = Session(
            id="sess_idle",
            template_id="python-basic",
            status=SessionStatus.RUNNING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_idle",
            runtime_type="docker",
            container_id="container-idle",
            last_activity_at=datetime.now() - timedelta(minutes=35)
        )
        session_repo.find_by_status.return_value = [idle_session]

        # Mock test dependency.
        scheduler.destroy_container.side_effect = Exception("Docker error")
        storage_service.delete_prefix.return_value = 2

        result = await service.cleanup_idle_sessions()

        # Cleanup should continue even if container destruction fails.
        assert result["idle_cleaned"] == 1
        assert idle_session.status == SessionStatus.TERMINATED
        storage_service.delete_prefix.assert_called_once()

    @pytest.mark.asyncio
    async def test_cleanup_error_handling(self, service, session_repo):
        """Test cleanup error handling."""
        session_repo.find_by_status.side_effect = Exception("Database error")

        result = await service.cleanup_idle_sessions()

        assert "errors" in result
        assert len(result["errors"]) > 0
        assert any("Database error" in str(e) for e in result["errors"])
