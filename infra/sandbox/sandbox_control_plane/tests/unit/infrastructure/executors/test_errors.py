"""Unit tests for errors."""
import pytest

from src.infrastructure.executors.errors import (
    ExecutorError,
    ExecutorConnectionError,
    ExecutorTimeoutError,
    ExecutorUnavailableError,
    ExecutorResponseError,
    ExecutorValidationError,
)


class TestExecutorError:
    """Tests for TestExecutorError."""

    def test_is_exception(self):
        """Test is exception."""
        error = ExecutorError("Test error")
        assert isinstance(error, Exception)

    def test_message(self):
        """Test message."""
        error = ExecutorError("Test error message")
        assert str(error) == "Test error message"


class TestExecutorConnectionError:
    """Tests for TestExecutorConnectionError."""

    def test_init(self):
        """Test init."""
        error = ExecutorConnectionError(
            executor_url="http://localhost:8080",
            reason="Connection refused"
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.reason == "Connection refused"

    def test_message_format(self):
        """Test message format."""
        error = ExecutorConnectionError(
            executor_url="http://localhost:8080",
            reason="Connection refused"
        )

        assert "http://localhost:8080" in str(error)
        assert "Connection refused" in str(error)

    def test_inherits_from_executor_error(self):
        """Test inherits from executor error."""
        error = ExecutorConnectionError("http://localhost:8080", "test")
        assert isinstance(error, ExecutorError)

    def test_empty_reason(self):
        """Test empty reason."""
        error = ExecutorConnectionError("http://localhost:8080")

        assert error.executor_url == "http://localhost:8080"
        assert error.reason == ""
        assert "http://localhost:8080" in str(error)


class TestExecutorTimeoutError:
    """Tests for TestExecutorTimeoutError."""

    def test_init(self):
        """Test init."""
        error = ExecutorTimeoutError(
            executor_url="http://localhost:8080",
            timeout=30.0
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.timeout == 30.0

    def test_message_format(self):
        """Test message format."""
        error = ExecutorTimeoutError(
            executor_url="http://localhost:8080",
            timeout=30.0
        )

        assert "http://localhost:8080" in str(error)
        assert "30.0" in str(error)
        assert "timed out" in str(error).lower()

    def test_inherits_from_executor_error(self):
        """Test inherits from executor error."""
        error = ExecutorTimeoutError("http://localhost:8080", 30.0)
        assert isinstance(error, ExecutorError)


class TestExecutorUnavailableError:
    """Tests for TestExecutorUnavailableError."""

    def test_init(self):
        """Test init."""
        error = ExecutorUnavailableError(
            executor_url="http://localhost:8080",
            status="unhealthy"
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.status == "unhealthy"

    def test_message_format(self):
        """Test message format."""
        error = ExecutorUnavailableError(
            executor_url="http://localhost:8080",
            status="unhealthy"
        )

        assert "http://localhost:8080" in str(error)
        assert "unavailable" in str(error).lower()
        assert "unhealthy" in str(error)

    def test_empty_status(self):
        """Test empty status."""
        error = ExecutorUnavailableError("http://localhost:8080")

        assert error.executor_url == "http://localhost:8080"
        assert error.status == ""

    def test_inherits_from_executor_error(self):
        """Test inherits from executor error."""
        error = ExecutorUnavailableError("http://localhost:8080", "test")
        assert isinstance(error, ExecutorError)


class TestExecutorResponseError:
    """Tests for TestExecutorResponseError."""

    def test_init(self):
        """Test init."""
        error = ExecutorResponseError(
            executor_url="http://localhost:8080",
            status_code=500,
            message="Internal Server Error"
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.status_code == 500
        assert error.message == "Internal Server Error"

    def test_message_format(self):
        """Test message format."""
        error = ExecutorResponseError(
            executor_url="http://localhost:8080",
            status_code=500,
            message="Internal Server Error"
        )

        assert "http://localhost:8080" in str(error)
        assert "500" in str(error)
        assert "Internal Server Error" in str(error)

    def test_empty_message(self):
        """Test empty message."""
        error = ExecutorResponseError(
            executor_url="http://localhost:8080",
            status_code=404
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.status_code == 404
        assert error.message == ""

    def test_inherits_from_executor_error(self):
        """Test inherits from executor error."""
        error = ExecutorResponseError("http://localhost:8080", 500, "test")
        assert isinstance(error, ExecutorError)


class TestExecutorValidationError:
    """Tests for TestExecutorValidationError."""

    def test_init(self):
        """Test init."""
        error = ExecutorValidationError(
            executor_url="http://localhost:8080",
            validation_errors=["Missing field: code", "Invalid timeout"]
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.validation_errors == ["Missing field: code", "Invalid timeout"]

    def test_message_format(self):
        """Test message format."""
        error = ExecutorValidationError(
            executor_url="http://localhost:8080",
            validation_errors=["Missing field: code"]
        )

        assert "http://localhost:8080" in str(error)
        assert "rejected request" in str(error).lower()
        assert "Missing field: code" in str(error)

    def test_empty_validation_errors(self):
        """Test empty validation errors."""
        error = ExecutorValidationError(
            executor_url="http://localhost:8080",
            validation_errors=[]
        )

        assert error.executor_url == "http://localhost:8080"
        assert error.validation_errors == []

    def test_inherits_from_executor_error(self):
        """Test inherits from executor error."""
        error = ExecutorValidationError("http://localhost:8080", [])
        assert isinstance(error, ExecutorError)
