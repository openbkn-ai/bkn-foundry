"""Unit tests for execution state."""
import pytest

from src.domain.value_objects.execution_status import (
    SessionStatus,
    ExecutionStatus,
    ExecutionState
)


class TestSessionStatus:
    """Tests for TestSessionStatus."""

    def test_session_status_values(self):
        """Test session status values."""
        assert SessionStatus.CREATING == "creating"
        assert SessionStatus.RUNNING == "running"
        assert SessionStatus.COMPLETED == "completed"
        assert SessionStatus.FAILED == "failed"
        assert SessionStatus.TIMEOUT == "timeout"
        assert SessionStatus.TERMINATED == "terminated"

    def test_session_status_is_string(self):
        """Test session status is string."""
        assert isinstance(SessionStatus.RUNNING, str)


class TestExecutionStatus:
    """Tests for TestExecutionStatus."""

    def test_execution_status_values(self):
        """Test execution status values."""
        assert ExecutionStatus.PENDING == "pending"
        assert ExecutionStatus.RUNNING == "running"
        assert ExecutionStatus.COMPLETED == "completed"
        assert ExecutionStatus.FAILED == "failed"
        assert ExecutionStatus.TIMEOUT == "timeout"
        assert ExecutionStatus.CRASHED == "crashed"

    def test_execution_status_is_string(self):
        """Test execution status is string."""
        assert isinstance(ExecutionStatus.RUNNING, str)


class TestExecutionState:
    """Tests for TestExecutionState."""

    def test_create_pending_state(self):
        """Test create pending state."""
        state = ExecutionState(status=ExecutionStatus.PENDING)

        assert state.status == ExecutionStatus.PENDING
        assert state.exit_code is None
        assert state.error_message is None

    def test_create_running_state(self):
        """Test create running state."""
        state = ExecutionState(status=ExecutionStatus.RUNNING)

        assert state.status == ExecutionStatus.RUNNING
        assert state.is_terminal() is False

    def test_create_completed_state(self):
        """Test create completed state."""
        state = ExecutionState(
            status=ExecutionStatus.COMPLETED,
            exit_code=0
        )

        assert state.status == ExecutionStatus.COMPLETED
        assert state.exit_code == 0
        assert state.is_terminal() is True
        assert state.can_retry() is False

    def test_create_failed_state_with_error(self):
        """Test create failed state with error."""
        state = ExecutionState(
            status=ExecutionStatus.FAILED,
            exit_code=1,
            error_message="Syntax error"
        )

        assert state.status == ExecutionStatus.FAILED
        assert state.exit_code == 1
        assert state.error_message == "Syntax error"
        assert state.is_terminal() is True
        assert state.can_retry() is False

    def test_create_failed_state_without_error(self):
        """Test create failed state without error."""
        state = ExecutionState(
            status=ExecutionStatus.FAILED,
            exit_code=1
        )

        assert state.status == ExecutionStatus.FAILED
        assert state.exit_code == 1
        assert state.error_message is None
        assert state.is_terminal() is True

    def test_create_timeout_state(self):
        """Test create timeout state."""
        state = ExecutionState(
            status=ExecutionStatus.TIMEOUT,
            exit_code=124
        )

        assert state.status == ExecutionStatus.TIMEOUT
        assert state.is_terminal() is True
        assert state.can_retry() is False

    def test_create_crashed_state(self):
        """Test create crashed state."""
        state = ExecutionState(
            status=ExecutionStatus.CRASHED
        )

        assert state.status == ExecutionStatus.CRASHED
        assert state.is_terminal() is False
        assert state.can_retry() is True

    def test_is_terminal_for_pending(self):
        """Test is terminal for pending."""
        state = ExecutionState(status=ExecutionStatus.PENDING)
        assert state.is_terminal() is False

    def test_is_terminal_for_running(self):
        """Test is terminal for running."""
        state = ExecutionState(status=ExecutionStatus.RUNNING)
        assert state.is_terminal() is False

    def test_is_terminal_for_completed(self):
        """Test is terminal for completed."""
        state = ExecutionState(status=ExecutionStatus.COMPLETED)
        assert state.is_terminal() is True

    def test_is_terminal_for_failed(self):
        """Test is terminal for failed."""
        state = ExecutionState(status=ExecutionStatus.FAILED)
        assert state.is_terminal() is True

    def test_is_terminal_for_timeout(self):
        """Test is terminal for timeout."""
        state = ExecutionState(status=ExecutionStatus.TIMEOUT)
        assert state.is_terminal() is True

    def test_can_retry_for_crashed(self):
        """Test can retry for crashed."""
        state = ExecutionState(status=ExecutionStatus.CRASHED)
        assert state.can_retry() is True

    def test_can_retry_for_completed(self):
        """Test can retry for completed."""
        state = ExecutionState(status=ExecutionStatus.COMPLETED)
        assert state.can_retry() is False

    def test_can_retry_for_failed(self):
        """Test can retry for failed."""
        state = ExecutionState(status=ExecutionStatus.FAILED)
        assert state.can_retry() is False

    def test_can_retry_for_timeout(self):
        """Test can retry for timeout."""
        state = ExecutionState(status=ExecutionStatus.TIMEOUT)
        assert state.can_retry() is False

    def test_immutability(self):
        """Test immutability."""
        state = ExecutionState(
            status=ExecutionStatus.PENDING,
            exit_code=0,
            error_message="test"
        )

        # frozen=True means the object cannot be modified.
        with pytest.raises(Exception):  # FrozenInstanceError
            state.exit_code = 1

    def test_state_equality(self):
        """Test state equality."""
        state1 = ExecutionState(
            status=ExecutionStatus.COMPLETED,
            exit_code=0
        )
        state2 = ExecutionState(
            status=ExecutionStatus.COMPLETED,
            exit_code=0
        )
        assert state1 == state2

        state3 = ExecutionState(
            status=ExecutionStatus.COMPLETED,
            exit_code=1
        )
        assert state1 != state3

    def test_all_terminal_statuses(self):
        """Test all terminal statuses."""
        terminal_states = [
            ExecutionStatus.COMPLETED,
            ExecutionStatus.FAILED,
            ExecutionStatus.TIMEOUT
        ]

        for status in terminal_states:
            state = ExecutionState(status=status)
            assert state.is_terminal() is True

    def test_non_terminal_statuses(self):
        """Test non terminal statuses."""
        non_terminal_states = [
            ExecutionStatus.PENDING,
            ExecutionStatus.RUNNING,
            ExecutionStatus.CRASHED
        ]

        for status in non_terminal_states:
            state = ExecutionState(status=status)
            assert state.is_terminal() is False
