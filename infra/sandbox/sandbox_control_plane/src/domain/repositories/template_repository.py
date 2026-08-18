"""
Template repository interface

The port for template persistence.
"""

from abc import ABC, abstractmethod
from typing import List, Optional

from src.domain.entities.template import Template


class ITemplateRepository(ABC):
    """
    Template repository interface

    The port the domain layer defines; the infrastructure layer supplies the adapter.
    """

    @abstractmethod
    async def save(self, template: Template) -> None:
        """Save the template, creating or updating it"""
        pass

    @abstractmethod
    async def find_by_id(self, template_id: str) -> Optional[Template]:
        """Find a template by id"""
        pass

    @abstractmethod
    async def find_by_name(self, name: str) -> Optional[Template]:
        """Find a template by name"""
        pass

    @abstractmethod
    async def find_all(self, offset: int = 0, limit: int = 100) -> List[Template]:
        """Find every template"""
        pass

    @abstractmethod
    async def delete(self, template_id: str) -> None:
        """Delete the template"""
        pass

    @abstractmethod
    async def exists(self, template_id: str) -> bool:
        """Check whether the template exists"""
        pass

    @abstractmethod
    async def exists_by_name(self, name: str) -> bool:
        """Check whether the template name exists"""
        pass

    @abstractmethod
    async def count(self) -> int:
        """Count the templates"""
        pass
