"""
Create-session command

The command object for creating a session.
Extended for dependency installation, following section 5 of sandbox-design-v2.1.md.
"""

import re
from dataclasses import dataclass, field
from typing import Dict, List, Optional

from src.domain.value_objects.resource_limit import ResourceLimit
from src.shared.utils.dependencies import DEFAULT_PYTHON_PACKAGE_INDEX_URL


@dataclass
class CreateSessionCommand:
    """
    Create-session command

    Extended to support installing Python dependencies.
    """

    template_id: Optional[str] = None
    timeout: int = 300
    resource_limit: ResourceLimit | None = None
    env_vars: Dict[str, str] | None = None
    id: Optional[str] = None  # session id, optionally given by the caller

    # Dependency installation fields
    dependencies: List[str] = field(default_factory=list)
    install_timeout: int = 300
    fail_on_dependency_error: bool = True
    allow_version_conflicts: bool = False
    python_package_index_url: str = DEFAULT_PYTHON_PACKAGE_INDEX_URL

    def __post_init__(self):
        """Validate after construction"""
        if self.timeout <= 0:
            raise ValueError("timeout must be positive")

        # Security critical: id and template_id travel through workspace_path into the
        # s3fs mount script that runs as root (the k8s and docker schedulers). The strict
        # allowlist is the backstop against shell command injection and prefix escape; the
        # request schema already validates at the entrance, and this covers the call paths
        # that bypass the schema.
        for _name, _val in (("id", self.id), ("template_id", self.template_id)):
            if _val is not None and not re.match(r"^[A-Za-z0-9_-]+$", _val):
                raise ValueError(
                    f"{_name} may only contain letters, digits, '_' and '-'"
                )

        # Apply the defaults
        if self.resource_limit is None:
            self.resource_limit = ResourceLimit.default()
        if self.env_vars is None:
            self.env_vars = {}

        # Validate the install timeout
        if self.install_timeout < 30 or self.install_timeout > 1800:
            raise ValueError("install_timeout must be between 30 and 1800 seconds")
