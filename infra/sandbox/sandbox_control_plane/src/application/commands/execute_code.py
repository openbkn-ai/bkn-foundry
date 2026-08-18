"""
Execute-code command

The command object for executing code.
"""

from dataclasses import dataclass
import re
from pathlib import PurePosixPath
from typing import Literal, Optional


@dataclass
class ExecuteCodeCommand:
    """Execute-code command"""

    session_id: str
    code: str
    language: Literal["python", "javascript", "shell"]
    async_mode: bool = False
    stdin: Optional[str] = None
    timeout: int = 30
    event_data: Optional[dict] = None
    env_vars: Optional[dict] = None
    working_directory: Optional[str] = None

    def __post_init__(self):
        """Validate after construction"""
        if not self.code:
            raise ValueError("code cannot be empty")
        if self.timeout <= 0:
            raise ValueError("timeout must be positive")
        if self.language not in {"python", "javascript", "shell"}:
            raise ValueError(f"Unsupported language: {self.language}")
        if self.working_directory is not None:
            self.working_directory = self._normalize_working_directory(self.working_directory)

    @staticmethod
    def _normalize_working_directory(path: str) -> str:
        stripped = path.strip()
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
