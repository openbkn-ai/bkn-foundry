"""
Session entity

One sandbox session; this is the aggregate root.
"""

from dataclasses import dataclass, field
from datetime import datetime, timedelta
from typing import List, Optional

from src.domain.value_objects.resource_limit import ResourceLimit
from src.domain.value_objects.execution_status import SessionStatus
from src.domain.entities.execution import Execution
from src.shared.utils.dependencies import (
    DEFAULT_PYTHON_PACKAGE_INDEX_URL,
    merge_pip_specs,
    normalize_python_package_index_url,
)


@dataclass
class InstalledDependency:
    """
    An installed dependency

    Tracks a package that is actually installed in the session.
    Follows section 5.6 of sandbox-design-v2.1.md.
    """

    name: str
    version: str
    install_location: str  # such as "/workspace/.venv/"
    install_time: datetime
    is_from_template: bool  # whether it came preinstalled with the template


@dataclass
class Session:
    """
    Session entity

    The aggregate root: owns the session lifecycle and its execution records.
    Extended for dependency installation, following section 5.6 of sandbox-design-v2.1.md.
    """

    id: str
    template_id: str
    status: SessionStatus
    resource_limit: ResourceLimit
    workspace_path: str
    runtime_type: str  # "docker" or "kubernetes"
    runtime_node: str | None = None
    container_id: str | None = None
    pod_name: str | None = None
    env_vars: dict = field(default_factory=dict)
    timeout: int = 300  # 5 minutes by default
    created_at: datetime = field(default_factory=datetime.now)
    updated_at: datetime = field(default_factory=datetime.now)
    completed_at: datetime | None = None
    last_activity_at: datetime = field(default_factory=datetime.now)
    _executions: List[Execution] = field(default_factory=list)

    # Dependency installation fields
    requested_dependencies: List[str] = field(default_factory=list)
    installed_dependencies: List[InstalledDependency] = field(default_factory=list)
    dependency_install_status: str = "pending"  # pending/installing/completed/failed
    dependency_install_error: Optional[str] = None
    python_package_index_url: str = DEFAULT_PYTHON_PACKAGE_INDEX_URL
    dependency_install_started_at: datetime | None = None
    dependency_install_completed_at: datetime | None = None

    def __post_init__(self):
        """Validate after construction"""
        if self.timeout <= 0:
            raise ValueError("timeout must be positive")
        if not self.workspace_path:
            raise ValueError("workspace_path cannot be empty")
        self.python_package_index_url = normalize_python_package_index_url(
            self.python_package_index_url
        )

    # ============== Domain behaviour ==============

    def mark_as_running(self, runtime_node: str, container_id: str) -> None:
        """Mark the session running"""
        if self.status != SessionStatus.CREATING:
            raise ValueError(f"Cannot mark session as running from status: {self.status}")

        self.status = SessionStatus.RUNNING
        self.runtime_node = runtime_node
        self.container_id = container_id
        self.updated_at = datetime.now()

    def mark_as_completed(self) -> None:
        """Mark the session completed"""
        if self.status != SessionStatus.RUNNING:
            raise ValueError(f"Cannot mark session as completed from status: {self.status}")

        self.status = SessionStatus.COMPLETED
        self.completed_at = datetime.now()
        self.updated_at = datetime.now()

    def mark_as_failed(self) -> None:
        """Mark the session failed"""
        if self.status not in {SessionStatus.CREATING, SessionStatus.RUNNING}:
            raise ValueError(f"Cannot mark session as failed from status: {self.status}")

        self.status = SessionStatus.FAILED
        self.completed_at = datetime.now()
        self.updated_at = datetime.now()

    def mark_as_terminated(self) -> None:
        """Terminate the session"""
        if self.status == SessionStatus.TERMINATED:
            return  # already in a terminal state

        self.status = SessionStatus.TERMINATED
        self.completed_at = datetime.now()
        self.updated_at = datetime.now()

    def update_last_activity(self) -> None:
        """Update the last activity time"""
        self.last_activity_at = datetime.now()
        self.updated_at = datetime.now()

    # ============== Domain queries ==============

    def is_active(self) -> bool:
        """Whether it is active"""
        return self.status in {SessionStatus.CREATING, SessionStatus.RUNNING}

    def is_terminated(self) -> bool:
        """Whether it has terminated"""
        return self.status == SessionStatus.TERMINATED

    def is_idle(self, threshold_minutes: int = 30) -> bool:
        """Whether it is idle, meaning inactive past the threshold"""
        if not self.is_active():
            return False
        idle_time = datetime.now() - self.last_activity_at
        return idle_time > timedelta(minutes=threshold_minutes)

    def is_expired(self, max_hours: int = 6) -> bool:
        """Whether it has expired, meaning older than the maximum lifetime"""
        age = datetime.now() - self.created_at
        return age > timedelta(hours=max_hours)

    def should_cleanup(self, idle_threshold_minutes: int = 30, max_lifetime_hours: int = 6) -> bool:
        """Whether it should be cleaned up"""
        return self.is_idle(idle_threshold_minutes) or self.is_expired(max_lifetime_hours)

    # ============== Execution management ==============

    def add_execution(self, execution: Execution) -> None:
        """Add an execution record"""
        if execution.session_id != self.id:
            raise ValueError("Execution does not belong to this session")
        self._executions.append(execution)
        self.update_last_activity()

    def get_executions(self) -> List[Execution]:
        """Get every execution record"""
        return list(self._executions)

    def get_running_executions(self) -> List[Execution]:
        """Get the running executions"""
        return [e for e in self._executions if e.is_running()]

    # ============== Dependency management ==============

    def replace_requested_dependencies(
        self, index_url: Optional[str], dependencies: List[str]
    ) -> None:
        """Replace the target dependency configuration wholesale."""
        self.python_package_index_url = normalize_python_package_index_url(index_url)
        self.requested_dependencies = list(dependencies)
        self.updated_at = datetime.now()

    def merge_requested_dependencies(
        self, index_url: Optional[str], dependencies: List[str]
    ) -> None:
        """Merge into the target dependency configuration."""
        self.python_package_index_url = normalize_python_package_index_url(
            index_url or self.python_package_index_url
        )
        self.requested_dependencies = merge_pip_specs(
            self.requested_dependencies,
            dependencies,
        )
        self.updated_at = datetime.now()

    def set_dependencies_installing(self) -> None:
        """Legacy interface: mark the dependency install as in progress."""
        self.mark_dependency_installing()

    def mark_dependency_installing(self, started_at: datetime | None = None) -> None:
        """Mark the dependency install as in progress."""
        self.dependency_install_status = "installing"
        self.dependency_install_started_at = started_at or datetime.now()
        self.dependency_install_completed_at = None
        self.dependency_install_error = None
        self.updated_at = datetime.now()

    def set_dependencies_completed(self, installed: List[InstalledDependency]) -> None:
        """Legacy interface: mark the dependency install as finished."""
        self.mark_dependency_install_completed(installed)

    def mark_dependency_install_completed(
        self,
        installed: List[InstalledDependency],
        completed_at: datetime | None = None,
    ) -> None:
        """Mark the dependency install as finished."""
        self.dependency_install_status = "completed"
        self.installed_dependencies = installed
        self.dependency_install_error = None
        self.dependency_install_completed_at = completed_at or datetime.now()
        self.updated_at = datetime.now()

    def set_dependencies_failed(self, error: str) -> None:
        """Legacy interface: mark the dependency install as failed."""
        self.mark_dependency_install_failed(error)

    def mark_dependency_install_failed(
        self,
        error: str,
        completed_at: datetime | None = None,
    ) -> None:
        """Mark the dependency install as failed."""
        self.dependency_install_status = "failed"
        self.dependency_install_error = error
        self.dependency_install_completed_at = completed_at or datetime.now()
        self.updated_at = datetime.now()

    def has_dependencies(self) -> bool:
        """Whether any dependency still has to be installed"""
        return len(self.requested_dependencies) > 0

    def is_dependency_install_pending(self) -> bool:
        """Whether a dependency install is pending or running"""
        return self.dependency_install_status in ("pending", "installing")

    def is_dependency_install_successful(self) -> bool:
        """Whether the dependency install succeeded"""
        return self.dependency_install_status == "completed"
