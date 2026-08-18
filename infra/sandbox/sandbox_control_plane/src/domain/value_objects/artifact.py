"""
File artifact value object

The metadata of a file an execution produced.
"""

from dataclasses import dataclass
from datetime import datetime
from typing import Literal
from enum import Enum


class ArtifactType(str, Enum):
    """Artifact type"""

    ARTIFACT = "artifact"  # a file the user generated
    LOG = "log"  # a log file
    OUTPUT = "output"  # a standard output file


@dataclass(frozen=True)
class Artifact:
    """File artifact value object, immutable"""

    path: str  # path relative to the workspace
    size: int  # file size in bytes
    mime_type: str  # MIME type
    type: ArtifactType
    created_at: datetime
    checksum: str | None = None  # SHA256 checksum

    def __post_init__(self):
        """Validate the artifact"""
        if self.size < 0:
            raise ValueError("size cannot be negative")
        if not self.path:
            raise ValueError("path cannot be empty")

    def is_log(self) -> bool:
        """Whether it is a log file"""
        return self.type == ArtifactType.LOG

    def is_output(self) -> bool:
        """Whether it is an output file"""
        return self.type == ArtifactType.OUTPUT

    @classmethod
    def create(
        cls,
        path: str,
        size: int,
        mime_type: str,
        type: Literal["artifact", "log", "output"] = "artifact",
        checksum: str | None = None,
    ) -> "Artifact":
        """Factory method: build an artifact"""
        return cls(
            path=path,
            size=size,
            mime_type=mime_type,
            type=ArtifactType(type),
            created_at=datetime.now(),
            checksum=checksum,
        )
