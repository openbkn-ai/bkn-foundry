"""
REST API request schemas

Defines the Pydantic request models FastAPI uses.
"""

import re
from pathlib import PurePosixPath
from pydantic import BaseModel, ConfigDict, Field, field_validator
from typing import Literal, Optional, Dict, List


class DependencySpec(BaseModel):
    """
    Dependency package specification

    Names a Python package to install when a session is created.
    Follows section 5.3.1 of sandbox-design-v2.1.md.
    """

    name: str = Field(..., min_length=1, max_length=100, description="Package name")
    version: Optional[str] = Field(None, description="Version constraint, such as ==2.31.0 or >=1.0")

    @field_validator("name")
    @classmethod
    def validate_package_name(cls, v: str) -> str:
        """
        Validate the package name format

        Rejects:
        - path traversal (..)
        - an absolute path (/)
        - URL (://)
        - illegal characters; only letters, digits, and ._- are allowed
        - a version mixed into the name, such as pandas2.3.3, which should be name="pandas", version="==2.3.3"
        """
        # Reject path traversal
        if ".." in v or v.startswith("/"):
            raise ValueError("Package name cannot contain path traversal characters")
        # Reject a URL
        if "://" in v:
            raise ValueError("Package name cannot contain URL")
        # PyPI naming rules: letters, digits, and ._- only
        if not re.match(r"^[a-zA-Z0-9._-]+$", v):
            raise ValueError("Invalid package name format")

        # Catch the common mistake of a version glued onto the name (pandas2.3.3, numpy1.24.0).
        # The pattern is: letter, then digit, dot, digit, which looks like a version.
        if re.match(r"^[a-zA-Z]+[0-9]+\.[0-9]", v):
            # Split out the name and version for the error message
            package_name = re.sub(r"[0-9]+\.[0-9].*", "", v)
            version_num = re.sub(r"^[a-zA-Z]+", "", v)
            raise ValueError(
                f"Invalid package name '{v}'. It looks like a version number is mixed with the package name. "
                f"Use separate 'name' and 'version' fields: "
                f'{{"name": "{package_name}", "version": "=={version_num}"}}'
            )

        return v

    def to_pip_spec(self) -> str:
        """
        Convert to a pip requirement specifier

        A version with no operator gets == in front.
        For example version="2.3.3" becomes "==2.3.3".

        Returns:
            The pip specifier, such as "requests==2.31.0" or "pandas"
        """
        if self.version:
            # Check whether the version already starts with an operator.
            # pip accepts: ==, >=, <=, >, <, ~=, !=, ~, =
            version_operators = ("==", ">=", "<=", ">", "<", "~=", "!=", "~", "=")
            if not self.version.startswith(version_operators):
                # Without an operator, default to ==
                return f"{self.name}=={self.version}"
            return f"{self.name}{self.version}"
        return self.name


class CreateSessionRequest(BaseModel):
    """
    Create-session request

    Follows section 5.3.1 of sandbox-design-v2.1.md, extended for dependency installation.
    """

    id: Optional[str] = Field(
        None, min_length=1, max_length=64, description="Session id. Optional; when given it has to be unique."
    )
    template_id: Optional[str] = Field(
        None, min_length=1, max_length=64, description="Template id. Without it the default template configuration applies."
    )
    timeout: int = Field(300, ge=1, le=3600, description="Timeout in seconds")
    cpu: str = Field("1", description="CPU cores")
    memory: str = Field("512Mi", description="Memory limit")
    disk: str = Field("1Gi", description="Disk limit")
    env_vars: Dict[str, str] = Field(default_factory=dict, description="Environment variables")
    event: Optional[Dict] = Field(None, description="Event payload")

    # Dependency installation fields
    dependencies: List[DependencySpec] = Field(
        default_factory=list, max_length=50, description="Session-level dependency packages"
    )
    install_timeout: int = Field(300, ge=30, le=1800, description="Dependency install timeout in seconds")
    fail_on_dependency_error: bool = Field(True, description="Whether a failed dependency install aborts session creation")
    allow_version_conflicts: bool = Field(
        False, description="Whether a version conflict is allowed between the template preinstall and the requested package"
    )
    python_package_index_url: Optional[str] = Field(
        None, max_length=512, description="Python package index URL, https://pypi.org/simple/ by default"
    )

    @field_validator("id", "template_id")
    @classmethod
    def validate_identifier(cls, v: Optional[str]) -> Optional[str]:
        """
        A session id or template id may hold only letters, digits, underscores, and hyphens.

        Security critical: the id travels through workspace_path into the s3fs mount script
        that runs as root (the k8s and docker schedulers). Allowing a shell metacharacter
        would be root command injection, and allowing '/' or '..' would escape the prefix
        and break the isolation between sessions. A strict allowlist stops both at the entrance.
        """
        if v is None:
            return v
        if not re.match(r"^[A-Za-z0-9_-]+$", v):
            raise ValueError(
                "id and template_id may only contain letters, digits, '_' and '-'"
            )
        return v

    @field_validator("cpu")
    @classmethod
    def validate_cpu(cls, v: str) -> str:
        try:
            float(v)
        except ValueError:
            raise ValueError("Invalid cpu format")
        return v


class ExecuteCodeRequest(BaseModel):
    """Execute-code request"""

    code: str = Field(
        ...,
        min_length=1,
        max_length=102400,
        description="The code to run. With language=python it has to match the AWS Lambda handler shape; with language=shell it is the shell script body.",
    )
    language: Literal["python", "javascript", "shell"] = Field(..., description="Programming language")
    timeout: int = Field(30, ge=1, le=3600, description="Execution timeout in seconds")
    event: Optional[Dict] = Field(None, description="Event payload")
    env_vars: Dict[str, str] = Field(
        default_factory=dict,
        description=(
            "Environment variables for this execution, overriding the values set when the session was created. "
            "Sessions are pooled and reused, so anything that varies per execution, such as the caller identity, has to be sent every time."
        ),
    )
    working_directory: Optional[str] = Field(
        None, description="Optional working directory, relative to the workspace root. Without it the workspace root is used."
    )

    @field_validator("working_directory")
    @classmethod
    def validate_working_directory(cls, value: Optional[str]) -> Optional[str]:
        if value is None:
            return value

        stripped = value.strip()
        if not stripped or stripped.startswith("/") or "\\" in stripped:
            raise ValueError("working_directory must be a relative workspace path")
        if re.match(r"^[A-Za-z]:", stripped):
            raise ValueError("working_directory must be a relative workspace path")

        normalized = PurePosixPath(stripped).as_posix()
        parts = PurePosixPath(normalized).parts
        if any(part == ".." for part in parts):
            raise ValueError("working_directory must be a relative workspace path")

        if normalized.startswith("./"):
            normalized = normalized[2:]
        if not normalized:
            raise ValueError("working_directory must be a relative workspace path")
        return normalized

    model_config = ConfigDict(
        json_schema_extra={
            "examples": [
                {
                    "code": 'def handler(event):\n    name = event.get("name", "World")\n    return {"message": f"Hello, {name}!"}',
                    "language": "python",
                    "timeout": 10,
                    "event": {"name": "World"},
                },
                {
                    "code": 'def handler(event):\n    name = event.get("name", "World")\n    age = event.get("age", 0)\n    return {"message": f"Hello, {name}!", "age_doubled": age * 2}',
                    "language": "python",
                    "timeout": 30,
                    "event": {"name": "Alice", "age": 25},
                },
                {"code": "pwd && ls -la", "language": "shell", "timeout": 30},
                {
                    "code": "bash run.sh && python tools/build.py",
                    "language": "shell",
                    "timeout": 30,
                    "working_directory": "skill/mini-wiki",
                },
            ]
        }
    )


class TerminateSessionRequest(BaseModel):
    """Terminate-session request"""

    reason: Optional[str] = Field(None, description="Reason for terminating")


class InstallSessionDependenciesRequest(BaseModel):
    """Incremental Python dependency install request."""

    python_package_index_url: Optional[str] = Field(
        None,
        max_length=512,
        description="Python package index URL. Without it the current session setting applies.",
    )
    dependencies: List[DependencySpec] = Field(
        ...,
        min_length=1,
        max_length=50,
        description="Dependencies to install in this batch",
    )
    install_timeout: int = Field(
        300,
        ge=30,
        le=1800,
        description="Timeout in seconds for this dependency install",
    )


class CreateTemplateRequest(BaseModel):
    """Create-template request"""

    id: str = Field(..., min_length=1, max_length=64, description="Template id")
    name: str = Field(..., min_length=1, max_length=255, description="Template name")
    image_url: str = Field(..., min_length=1, max_length=512, description="Image URL")
    runtime_type: Literal["python3.11", "nodejs20", "java17", "go1.21"] = Field(
        ..., description="Runtime type"
    )
    default_cpu_cores: float = Field(0.5, ge=0.1, le=4.0, description="Default CPU cores")
    default_memory_mb: int = Field(512, ge=128, le=8192, description="Default memory in MB")
    default_disk_mb: int = Field(1024, ge=256, le=51200, description="Default disk in MB")
    default_timeout: int = Field(300, ge=60, le=3600, description="Default timeout in seconds")
    default_env_vars: Optional[Dict[str, str]] = Field(None, description="Default environment variables")


class UpdateTemplateRequest(BaseModel):
    """Update-template request"""

    name: Optional[str] = Field(None, min_length=1, max_length=255, description="Template name")
    image_url: Optional[str] = Field(None, min_length=1, max_length=512, description="Image URL")
    default_cpu_cores: Optional[float] = Field(None, ge=0.1, le=4.0, description="Default CPU cores")
    default_memory_mb: Optional[int] = Field(None, ge=128, le=8192, description="Default memory in MB")
    default_disk_mb: Optional[int] = Field(None, ge=256, le=51200, description="Default disk in MB")
    default_timeout: Optional[int] = Field(None, ge=60, le=3600, description="Default timeout in seconds")
    default_env_vars: Optional[Dict[str, str]] = Field(None, description="Default environment variables")
