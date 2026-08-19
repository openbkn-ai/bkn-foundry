"""Unit tests for session stuck creating service."""
import pytest
from unittest.mock import Mock, AsyncMock
from datetime import datetime, timedelta

from src.application.services.session_stuck_creating_service import SessionStuckCreatingService
from src.domain.entities.session import Session
from src.domain.value_objects.resource_limit import ResourceLimit
from src.domain.value_objects.execution_status import SessionStatus
from src.domain.repositories.session_repository import ISessionRepository


class TestSessionStuckCreatingService:
    """Tests for TestSessionStuckCreatingService."""

    @pytest.fixture
    def session_repo(self):
        """Create session repo."""
        repo = Mock()
        repo.save = AsyncMock()
        repo.find_by_id = AsyncMock()
        repo.find_by_status = AsyncMock()
        return repo

    @pytest.fixture
    def service(self, session_repo):
        """Create service."""
        return SessionStuckCreatingService(
            session_repo=session_repo,
            creating_timeout_seconds=300,  # Timing-related test setup.
        )

    @pytest.fixture
    def creating_session_stuck(self):
        """Create creating session stuck."""
        old_time = datetime.now() - timedelta(minutes=6)  # Timing-related test setup.
        return Session(
            id="sess_stuck",
            template_id="python-basic",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_stuck",
            runtime_type="docker",
            created_at=old_time,
        )

    @pytest.fixture
    def creating_session_recent(self):
        """Create creating session recent."""
        recent_time = datetime.now() - timedelta(minutes=2)  # Timing-related test setup.
        return Session(
            id="sess_recent",
            template_id="python-basic",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_recent",
            runtime_type="docker",
            created_at=recent_time,
        )

    @pytest.mark.asyncio
    async def test_mark_stuck_session_as_failed(self, service, session_repo, creating_session_stuck):
        """Test mark stuck session as failed."""
        session_repo.find_by_status.return_value = [creating_session_stuck]

        result = await service.check_and_mark_stuck_sessions()

        assert result["total_checked"] == 1
        assert result["marked_failed"] == 1
        assert creating_session_stuck.status == SessionStatus.FAILED
        session_repo.save.assert_called_once_with(creating_session_stuck)

    @pytest.mark.asyncio
    async def test_keep_recent_creating_session(self, service, session_repo, creating_session_recent):
        """Test keep recent creating session."""
        session_repo.find_by_status.return_value = [creating_session_recent]

        result = await service.check_and_mark_stuck_sessions()

        assert result["total_checked"] == 1
        assert result["marked_failed"] == 0
        assert creating_session_recent.status == SessionStatus.CREATING
        session_repo.save.assert_not_called()

    @pytest.mark.asyncio
    async def test_check_mixed_creating_sessions(self, service, session_repo):
        """Test check mixed creating sessions."""
        stuck_time = datetime.now() - timedelta(minutes=6)
        recent_time = datetime.now() - timedelta(minutes=2)

        session_repo.find_by_status.return_value = [
            Session(
                id="sess_1",
                template_id="python-basic",
                status=SessionStatus.CREATING,
                resource_limit=ResourceLimit.default(),
                workspace_path="s3://sandbox-workspace/sessions/sess_1",
                runtime_type="docker",
                created_at=stuck_time,  # Timing-related test setup.
            ),
            Session(
                id="sess_2",
                template_id="python-basic",
                status=SessionStatus.CREATING,
                resource_limit=ResourceLimit.default(),
                workspace_path="s3://sandbox-workspace/sessions/sess_2",
                runtime_type="docker",
                created_at=recent_time,  # Test setup.
            ),
        ]

        result = await service.check_and_mark_stuck_sessions()

        assert result["total_checked"] == 2
        assert result["marked_failed"] == 1

    @pytest.mark.asyncio
    async def test_no_creating_sessions(self, service, session_repo):
        """Test no creating sessions."""
        session_repo.find_by_status.return_value = []

        result = await service.check_and_mark_stuck_sessions()

        assert result["total_checked"] == 0
        assert result["marked_failed"] == 0

    @pytest.mark.asyncio
    async def test_custom_timeout_threshold(self, session_repo):
        """Test custom timeout threshold."""
        service = SessionStuckCreatingService(
            session_repo=session_repo,
            creating_timeout_seconds=60,  # Timing-related test setup.
        )

        # Timing-related test setup.
        old_time = datetime.now() - timedelta(minutes=2)
        stuck_session = Session(
            id="sess_stuck",
            template_id="python-basic",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_stuck",
            runtime_type="docker",
            created_at=old_time,
        )
        session_repo.find_by_status.return_value = [stuck_session]

        result = await service.check_and_mark_stuck_sessions()

        assert result["marked_failed"] == 1
        assert stuck_session.status == SessionStatus.FAILED

    @pytest.mark.asyncio
    async def test_error_handling(self, service, session_repo):
        """Test error handling."""
        session_repo.find_by_status.side_effect = Exception("Database error")

        result = await service.check_and_mark_stuck_sessions()

        assert "errors" in result
        assert len(result["errors"]) > 0
        assert any("Database error" in str(e) for e in result["errors"])

    @pytest.mark.asyncio
    async def test_session_without_created_at(self, service, session_repo):
        """Test session without created at."""
        session = Session(
            id="sess_no_created_at",
            template_id="python-basic",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_no_created_at",
            runtime_type="docker",
            created_at=None,  # Create test data.
        )
        session_repo.find_by_status.return_value = [session]

        result = await service.check_and_mark_stuck_sessions()

        # Verify expected behavior.
        assert result["marked_failed"] == 0
        assert session.status == SessionStatus.CREATING

    @pytest.mark.asyncio
    async def test_exactly_at_threshold(self, service, session_repo):
        """Test exactly at threshold."""
        # Timing-related test setup.
        exact_threshold_time = datetime.now() - timedelta(seconds=300)
        session = Session(
            id="sess_exact",
            template_id="python-basic",
            status=SessionStatus.CREATING,
            resource_limit=ResourceLimit.default(),
            workspace_path="s3://sandbox-workspace/sessions/sess_exact",
            runtime_type="docker",
            created_at=exact_threshold_time,
        )
        session_repo.find_by_status.return_value = [session]

        result = await service.check_and_mark_stuck_sessions()

        # Test setup.
        assert result["marked_failed"] == 1
