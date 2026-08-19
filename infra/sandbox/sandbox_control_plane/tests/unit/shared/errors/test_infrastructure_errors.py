"""Unit tests for infrastructure errors."""
import pytest

from src.shared.errors.infrastructure import (
    InfrastructureError,
    DatabaseError,
    ConnectionError,
    StorageError,
    HTTPClientError,
    ContainerError,
    KubernetesError,
    MessagingError,
)


class TestInfrastructureError:
    """Tests for TestInfrastructureError."""

    def test_is_exception(self):
        """Test is exception."""
        error = InfrastructureError("Test error")
        assert isinstance(error, Exception)

    def test_message(self):
        """Test message."""
        error = InfrastructureError("Test error message")
        assert str(error) == "Test error message"
        assert error.message == "Test error message"

    def test_with_original_error(self):
        """Test with original error."""
        original = ValueError("Original error")
        error = InfrastructureError("Wrapper error", original_error=original)

        assert error.message == "Wrapper error"
        assert error.original_error is original

    def test_without_original_error(self):
        """Test without original error."""
        error = InfrastructureError("Test error")

        assert error.message == "Test error"
        assert error.original_error is None


class TestDatabaseError:
    """Tests for TestDatabaseError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = DatabaseError("Database connection failed")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = DatabaseError("Connection timeout")
        assert str(error) == "Connection timeout"

    def test_with_original_error(self):
        """Test with original error."""
        original = ConnectionRefusedError("Connection refused")
        error = DatabaseError("Database error", original_error=original)

        assert error.original_error is original


class TestConnectionError:
    """Tests for TestConnectionError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = ConnectionError("Connection failed")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = ConnectionError("Failed to connect to server")
        assert str(error) == "Failed to connect to server"

    def test_with_original_error(self):
        """Test with original error."""
        original = TimeoutError("Connection timeout")
        error = ConnectionError("Connection error", original_error=original)

        assert error.original_error is original


class TestStorageError:
    """Tests for TestStorageError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = StorageError("Storage error")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = StorageError("Failed to upload file")
        assert str(error) == "Failed to upload file"

    def test_with_original_error(self):
        """Test with original error."""
        original = IOError("Disk full")
        error = StorageError("Storage error", original_error=original)

        assert error.original_error is original


class TestHTTPClientError:
    """Tests for TestHTTPClientError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = HTTPClientError("HTTP error")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = HTTPClientError("Request timeout")
        assert str(error) == "Request timeout"

    def test_with_original_error(self):
        """Test with original error."""
        original = Exception("Network error")
        error = HTTPClientError("HTTP client error", original_error=original)

        assert error.original_error is original


class TestContainerError:
    """Tests for TestContainerError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = ContainerError("Container error")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = ContainerError("Failed to start container")
        assert str(error) == "Failed to start container"

    def test_with_original_error(self):
        """Test with original error."""
        original = RuntimeError("Docker daemon not running")
        error = ContainerError("Container error", original_error=original)

        assert error.original_error is original


class TestKubernetesError:
    """Tests for TestKubernetesError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = KubernetesError("Kubernetes error")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = KubernetesError("Pod failed to start")
        assert str(error) == "Pod failed to start"

    def test_with_original_error(self):
        """Test with original error."""
        original = Exception("API server unavailable")
        error = KubernetesError("Kubernetes error", original_error=original)

        assert error.original_error is original


class TestMessagingError:
    """Tests for TestMessagingError."""

    def test_inherits_from_infrastructure_error(self):
        """Test inherits from infrastructure error."""
        error = MessagingError("Messaging error")
        assert isinstance(error, InfrastructureError)

    def test_message(self):
        """Test message."""
        error = MessagingError("Failed to publish message")
        assert str(error) == "Failed to publish message"

    def test_with_original_error(self):
        """Test with original error."""
        original = Exception("Queue not found")
        error = MessagingError("Messaging error", original_error=original)

        assert error.original_error is original


class TestErrorHierarchy:
    """Tests for TestErrorHierarchy."""

    def test_all_errors_inherit_from_infrastructure_error(self):
        """Test all errors inherit from infrastructure error."""
        errors = [
            DatabaseError("test"),
            ConnectionError("test"),
            StorageError("test"),
            HTTPClientError("test"),
            ContainerError("test"),
            KubernetesError("test"),
            MessagingError("test"),
        ]

        for error in errors:
            assert isinstance(error, InfrastructureError)
            assert isinstance(error, Exception)

    def test_errors_can_be_caught_by_base_class(self):
        """Test errors can be caught by base class."""
        errors_to_raise = [
            DatabaseError("db error"),
            ContainerError("container error"),
            StorageError("storage error"),
        ]

        for error in errors_to_raise:
            try:
                raise error
            except InfrastructureError as e:
                assert e is error
            except Exception:
                pytest.fail("Error should be caught by InfrastructureError")
