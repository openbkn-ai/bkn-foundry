"""
Resource limit value object

CPU, memory, and disk limits.
"""

from dataclasses import dataclass
from typing import Self


@dataclass(frozen=True)
class ResourceLimit:
    """Resource limit value object, immutable"""

    cpu: str  # such as "1", "2", or "0.5"
    memory: str  # such as "512Mi", "1Gi", or "2Gi"
    disk: str  # such as "1Gi" or "10Gi"
    max_processes: int = 128  # maximum processes

    def __post_init__(self):
        """Validate the resource limits"""
        if self.max_processes <= 0:
            raise ValueError("max_processes must be positive")

        # Validate the CPU format
        try:
            cpu_value = float(self.cpu)
        except ValueError:
            raise ValueError(f"Invalid cpu format: {self.cpu}")

        if cpu_value <= 0:
            raise ValueError("cpu must be positive")

        # Validate the memory format
        if not self._validate_size_format(self.memory):
            raise ValueError(f"Invalid memory format: {self.memory}")

        # Validate the disk format
        if not self._validate_size_format(self.disk):
            raise ValueError(f"Invalid disk format: {self.disk}")

    @staticmethod
    def _validate_size_format(size: str) -> bool:
        """Validate a size, such as 512Mi or 1Gi"""
        if not size:
            return False
        if size[-2:] in {"Mi", "Gi"}:
            try:
                int(size[:-2])
                return True
            except ValueError:
                return False
        return False

    def with_cpu(self, cpu: str) -> Self:
        """Return a new object with this CPU limit; the original is unchanged"""
        return ResourceLimit(
            cpu=cpu, memory=self.memory, disk=self.disk, max_processes=self.max_processes
        )

    def with_memory(self, memory: str) -> Self:
        """Return a new object with this memory limit; the original is unchanged"""
        return ResourceLimit(
            cpu=self.cpu, memory=memory, disk=self.disk, max_processes=self.max_processes
        )

    @classmethod
    def default(cls) -> Self:
        """The default resource limits"""
        return cls(cpu="1", memory="512Mi", disk="1Gi", max_processes=128)
