"""
Template entity

Defines a sandbox environment template.
"""

from dataclasses import dataclass, field
from datetime import datetime
from typing import List, Dict

from src.domain.value_objects.resource_limit import ResourceLimit


@dataclass
class Template:
    """
    Template entity

    The configuration template of a sandbox execution environment.
    """

    id: str
    name: str
    image: str  # Docker image
    base_image: str  # base image
    pre_installed_packages: List[str] = field(default_factory=list)
    default_resources: ResourceLimit = field(default_factory=ResourceLimit.default)
    default_timeout_sec: int = 300  # default timeout in seconds
    security_context: Dict = field(default_factory=dict)
    created_at: datetime = field(default_factory=datetime.now)
    updated_at: datetime = field(default_factory=datetime.now)

    def __post_init__(self):
        """Validate after construction"""
        if not self.name:
            raise ValueError("name cannot be empty")
        if not self.image:
            raise ValueError("image cannot be empty")
        if not self.base_image:
            raise ValueError("base_image cannot be empty")

    # ============== Domain behaviour ==============

    def update_name(self, name: str) -> None:
        """Update the name"""
        if not name:
            raise ValueError("name cannot be empty")
        self.name = name
        self.updated_at = datetime.now()

    def update_image(self, image: str) -> None:
        """Update the image"""
        if not image:
            raise ValueError("image cannot be empty")
        self.image = image
        self.updated_at = datetime.now()

    def add_package(self, package: str) -> None:
        """Add a preinstalled package"""
        if package not in self.pre_installed_packages:
            self.pre_installed_packages.append(package)
            self.updated_at = datetime.now()

    def remove_package(self, package: str) -> None:
        """Remove a preinstalled package"""
        if package in self.pre_installed_packages:
            self.pre_installed_packages.remove(package)
            self.updated_at = datetime.now()

    def update_default_resources(self, resources: ResourceLimit) -> None:
        """Update the default resource configuration"""
        self.default_resources = resources
        self.updated_at = datetime.now()

    def update_timeout(self, timeout_sec: int) -> None:
        """Update the default timeout"""
        if timeout_sec < 0:
            raise ValueError("timeout_sec must be non-negative")
        self.default_timeout_sec = timeout_sec
        self.updated_at = datetime.now()

    # ============== Domain queries ==============

    def has_package(self, package: str) -> bool:
        """Whether it holds a given package"""
        return package in self.pre_installed_packages

    def get_image_name(self) -> str:
        """The image name without the tag"""
        return self.image.split(":")[0] if ":" in self.image else self.image
