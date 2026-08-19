"""Unit tests for DTO."""
import pytest
from pydantic import ValidationError

from src.infrastructure.executors.dto import (
    ExecutorExecuteRequest,
    ExecutorExecuteResponse,
    ExecutorHealthResponse,
    ExecutorContainerInfo,
)


class TestExecutorExecuteRequest:
    """Tests for TestExecutorExecuteRequest."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="print('hello')",
            language="python"
        )

        assert request.execution_id == "exec-123"
        assert request.session_id == "sess-456"
        assert request.code == "print('hello')"
        assert request.language == "python"
        assert request.event == {}
        assert request.timeout == 300  # default
        assert request.env_vars == {}  # default
        assert request.working_directory is None

    def test_create_with_all_fields(self):
        """Test create with all fields."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="print('hello')",
            language="python",
            event={"name": "World"},
            timeout=60,
            env_vars={"DEBUG": "true"},
            working_directory="src/jobs",
        )

        assert request.execution_id == "exec-123"
        assert request.session_id == "sess-456"
        assert request.code == "print('hello')"
        assert request.language == "python"
        assert request.event == {"name": "World"}
        assert request.timeout == 60
        assert request.env_vars == {"DEBUG": "true"}
        assert request.working_directory == "src/jobs"

    def test_timeout_minimum(self):
        """Test timeout minimum."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="print('hello')",
            language="python",
            timeout=1
        )
        assert request.timeout == 1

    def test_timeout_maximum(self):
        """Test timeout maximum."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="print('hello')",
            language="python",
            timeout=3600
        )
        assert request.timeout == 3600

    def test_timeout_below_minimum(self):
        """Test timeout below minimum."""
        with pytest.raises(ValidationError):
            ExecutorExecuteRequest(
                execution_id="exec-123",
                session_id="sess-456",
                code="print('hello')",
                language="python",
                timeout=0
            )

    def test_timeout_above_maximum(self):
        """Test timeout above maximum."""
        with pytest.raises(ValidationError):
            ExecutorExecuteRequest(
                execution_id="exec-123",
                session_id="sess-456",
                code="print('hello')",
                language="python",
                timeout=3601
            )

    def test_missing_required_fields(self):
        """Test missing required fields."""
        with pytest.raises(ValidationError):
            ExecutorExecuteRequest(
                execution_id="exec-123",
                # missing session_id
                code="print('hello')",
                language="python"
            )

    def test_model_dump(self):
        """Test model dump."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="print('hello')",
            language="python",
            event={"name": "World"},
            timeout=60
        )

        data = request.model_dump()

        assert data["execution_id"] == "exec-123"
        assert data["session_id"] == "sess-456"
        assert data["code"] == "print('hello')"
        assert data["language"] == "python"
        assert data["event"] == {"name": "World"}
        assert data["timeout"] == 60

    def test_model_dump_includes_working_directory(self):
        """Test model dump includes working directory."""
        request = ExecutorExecuteRequest(
            execution_id="exec-123",
            session_id="sess-456",
            code="echo hello",
            language="shell",
            working_directory="skill/mini-wiki",
        )

        data = request.model_dump()

        assert data["working_directory"] == "skill/mini-wiki"

    def test_json_schema_examples(self):
        """Test JSON schema examples."""
        # Should have examples defined
        assert "examples" in ExecutorExecuteRequest.model_config.get("json_schema_extra", {})


class TestExecutorExecuteResponse:
    """Tests for TestExecutorExecuteResponse."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        response = ExecutorExecuteResponse(
            execution_id="exec-123",
            status="submitted"
        )

        assert response.execution_id == "exec-123"
        assert response.status == "submitted"
        assert response.message == ""  # default

    def test_create_with_all_fields(self):
        """Test create with all fields."""
        response = ExecutorExecuteResponse(
            execution_id="exec-123",
            status="completed",
            message="Execution finished successfully"
        )

        assert response.execution_id == "exec-123"
        assert response.status == "completed"
        assert response.message == "Execution finished successfully"

    def test_missing_required_fields(self):
        """Test missing required fields."""
        with pytest.raises(ValidationError):
            ExecutorExecuteResponse(
                execution_id="exec-123"
                # missing status
            )

    def test_from_json(self):
        """Test from JSON."""
        response = ExecutorExecuteResponse(**{
            "execution_id": "exec-123",
            "status": "completed",
            "message": "Done"
        })

        assert response.execution_id == "exec-123"
        assert response.status == "completed"
        assert response.message == "Done"


class TestExecutorHealthResponse:
    """Tests for TestExecutorHealthResponse."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        response = ExecutorHealthResponse(
            status="healthy"
        )

        assert response.status == "healthy"
        assert response.version == "1.0.0"  # default
        assert response.uptime_seconds is None
        assert response.active_executions is None

    def test_create_with_all_fields(self):
        """Test create with all fields."""
        response = ExecutorHealthResponse(
            status="healthy",
            version="2.0.0",
            uptime_seconds=3600.5,
            active_executions=5
        )

        assert response.status == "healthy"
        assert response.version == "2.0.0"
        assert response.uptime_seconds == 3600.5
        assert response.active_executions == 5

    def test_unhealthy_status(self):
        """Test unhealthy status."""
        response = ExecutorHealthResponse(
            status="unhealthy"
        )

        assert response.status == "unhealthy"

    def test_from_json(self):
        """Test from JSON."""
        response = ExecutorHealthResponse(**{
            "status": "healthy",
            "version": "1.5.0",
            "uptime_seconds": 100.0,
            "active_executions": 3
        })

        assert response.status == "healthy"
        assert response.version == "1.5.0"
        assert response.uptime_seconds == 100.0
        assert response.active_executions == 3

    def test_from_json_minimal(self):
        """Test from JSON minimal."""
        response = ExecutorHealthResponse(**{
            "status": "healthy"
        })

        assert response.status == "healthy"
        assert response.version == "1.0.0"


class TestExecutorContainerInfo:
    """Tests for TestExecutorContainerInfo."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        info = ExecutorContainerInfo(
            container_id="container-123",
            container_name="sandbox-sess-123"
        )

        assert info.container_id == "container-123"
        assert info.container_name == "sandbox-sess-123"
        assert info.executor_port == 8080  # default

    def test_create_with_custom_port(self):
        """Test create with custom port."""
        info = ExecutorContainerInfo(
            container_id="container-123",
            container_name="sandbox-sess-123",
            executor_port=9090
        )

        assert info.executor_port == 9090

    def test_executor_url_default_port(self):
        """Test executor URL default port."""
        info = ExecutorContainerInfo(
            container_id="container-123",
            container_name="sandbox-sess-123"
        )

        assert info.executor_url == "http://sandbox-sess-123:8080"

    def test_executor_url_custom_port(self):
        """Test executor URL custom port."""
        info = ExecutorContainerInfo(
            container_id="container-123",
            container_name="sandbox-sess-123",
            executor_port=9090
        )

        assert info.executor_url == "http://sandbox-sess-123:9090"

    def test_executor_url_with_underscored_name(self):
        """Test executor URL with underscored name."""
        info = ExecutorContainerInfo(
            container_id="container-123",
            container_name="sandbox_session_test"
        )

        assert info.executor_url == "http://sandbox_session_test:8080"

    def test_is_dataclass(self):
        """Test is dataclass."""
        from dataclasses import is_dataclass

        assert is_dataclass(ExecutorContainerInfo)
