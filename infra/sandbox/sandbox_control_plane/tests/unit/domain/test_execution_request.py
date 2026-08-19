"""Unit tests for execution request."""
import pytest

from src.domain.value_objects.execution_request import ExecutionRequest


class TestExecutionRequest:
    """Tests for TestExecutionRequest."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={"name": "World"},
            timeout=60,
            env_vars={"DEBUG": "true"}
        )

        assert request.code == "print('hello')"
        assert request.language == "python"
        assert request.event == {"name": "World"}
        assert request.timeout == 60
        assert request.env_vars == {"DEBUG": "true"}
        assert request.execution_id is None
        assert request.session_id is None

    def test_create_with_all_fields(self):
        """Test create with all fields."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={"name": "World"},
            timeout=60,
            env_vars={"DEBUG": "true"},
            execution_id="exec-123",
            session_id="sess-456",
            working_directory="src/tasks",
        )

        assert request.execution_id == "exec-123"
        assert request.session_id == "sess-456"
        assert request.working_directory == "src/tasks"

    def test_language_python(self):
        """Test language python."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request.language == "python"

    def test_language_javascript(self):
        """Test language javascript."""
        request = ExecutionRequest(
            code="console.log('hello')",
            language="javascript",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request.language == "javascript"

    def test_language_shell(self):
        """Test language shell."""
        request = ExecutionRequest(
            code="echo hello",
            language="shell",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request.language == "shell"

    def test_empty_code_raises_error(self):
        """Test empty code raises error."""
        with pytest.raises(ValueError, match="code cannot be empty"):
            ExecutionRequest(
                code="",
                language="python",
                event={},
                timeout=60,
                env_vars={}
            )

    def test_empty_language_raises_error(self):
        """Test empty language raises error."""
        with pytest.raises(ValueError, match="language cannot be empty"):
            ExecutionRequest(
                code="print('hello')",
                language="",
                event={},
                timeout=60,
                env_vars={}
            )

    def test_timeout_below_minimum_raises_error(self):
        """Test timeout below minimum raises error."""
        with pytest.raises(ValueError, match="timeout must be between 1 and 3600"):
            ExecutionRequest(
                code="print('hello')",
                language="python",
                event={},
                timeout=0,
                env_vars={}
            )

    def test_timeout_above_maximum_raises_error(self):
        """Test timeout above maximum raises error."""
        with pytest.raises(ValueError, match="timeout must be between 1 and 3600"):
            ExecutionRequest(
                code="print('hello')",
                language="python",
                event={},
                timeout=3601,
                env_vars={}
            )

    def test_timeout_negative_raises_error(self):
        """Test timeout negative raises error."""
        with pytest.raises(ValueError, match="timeout must be between 1 and 3600"):
            ExecutionRequest(
                code="print('hello')",
                language="python",
                event={},
                timeout=-1,
                env_vars={}
            )

    def test_unsupported_language_raises_error(self):
        """Test unsupported language raises error."""
        with pytest.raises(ValueError, match="unsupported language"):
            ExecutionRequest(
                code="print('hello')",
                language="ruby",
                event={},
                timeout=60,
                env_vars={}
            )

    def test_timeout_boundary_minimum(self):
        """Test timeout boundary minimum."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=1,
            env_vars={}
        )
        assert request.timeout == 1

    def test_timeout_boundary_maximum(self):
        """Test timeout boundary maximum."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=3600,
            env_vars={}
        )
        assert request.timeout == 3600

    def test_env_vars_empty(self):
        """Test env vars empty."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request.env_vars == {}

    def test_env_vars_with_values(self):
        """Test env vars with values."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={"DEBUG": "true", "API_KEY": "secret"}
        )
        assert request.env_vars == {"DEBUG": "true", "API_KEY": "secret"}

    def test_event_empty(self):
        """Test event empty."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request.event == {}

    def test_event_with_nested_data(self):
        """Test event with nested data."""
        event = {
            "name": "World",
            "data": {"key": "value"},
            "list": [1, 2, 3]
        }
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event=event,
            timeout=60,
            env_vars={}
        )
        assert request.event == event

    def test_working_directory_normalized(self):
        """Test working directory normalized."""
        request = ExecutionRequest(
            code="echo hello",
            language="shell",
            event={},
            timeout=60,
            env_vars={},
            working_directory="./skill/mini-wiki",
        )

        assert request.working_directory == "skill/mini-wiki"

    def test_invalid_working_directory_raises_error(self):
        """Test invalid working directory raises error."""
        with pytest.raises(ValueError, match="working_directory must be a relative workspace path"):
            ExecutionRequest(
                code="echo hello",
                language="shell",
                event={},
                timeout=60,
                env_vars={},
                working_directory="../etc",
            )

    def test_is_dataclass(self):
        """Test is dataclass."""
        from dataclasses import is_dataclass
        assert is_dataclass(ExecutionRequest)

    def test_dataclass_equality(self):
        """Test dataclass equality."""
        request1 = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        request2 = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request1 == request2

    def test_dataclass_inequality(self):
        """Test dataclass inequality."""
        request1 = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        request2 = ExecutionRequest(
            code="print('world')",
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert request1 != request2

    def test_multiline_code(self):
        """Test multiline code."""
        code = '''
def greet(name):
    return f"Hello, {name}!"

print(greet("World"))
'''
        request = ExecutionRequest(
            code=code,
            language="python",
            event={},
            timeout=60,
            env_vars={}
        )
        assert "def greet" in request.code

    def test_optional_execution_id(self):
        """Test optional execution ID."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={},
            execution_id="exec-123"
        )
        assert request.execution_id == "exec-123"

    def test_optional_session_id(self):
        """Test optional session ID."""
        request = ExecutionRequest(
            code="print('hello')",
            language="python",
            event={},
            timeout=60,
            env_vars={},
            session_id="sess-456"
        )
        assert request.session_id == "sess-456"
