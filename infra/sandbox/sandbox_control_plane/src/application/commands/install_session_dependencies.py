"""
Incremental session dependency install command.
"""

from dataclasses import dataclass
from typing import Optional


@dataclass
class InstallSessionDependenciesCommand:
    """Incremental session dependency install command."""

    session_id: str
    dependencies: list[str]
    python_package_index_url: Optional[str] = None
    install_timeout: int = 300

    def __post_init__(self):
        """Validate after construction."""
        if self.install_timeout < 30 or self.install_timeout > 1800:
            raise ValueError("install_timeout must be between 30 and 1800 seconds")
