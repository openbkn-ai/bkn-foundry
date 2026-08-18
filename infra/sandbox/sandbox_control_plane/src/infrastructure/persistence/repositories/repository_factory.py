"""
Repository factory

Provides repository instances bound to a database session.
"""

from contextlib import asynccontextmanager
from typing import AsyncGenerator

from src.infrastructure.persistence.database import db_manager
from src.infrastructure.persistence.repositories.sql_session_repository import SqlSessionRepository
from src.infrastructure.persistence.repositories.sql_execution_repository import (
    SqlExecutionRepository,
)
from src.infrastructure.persistence.repositories.sql_template_repository import (
    SqlTemplateRepository,
)

from src.domain.repositories.session_repository import ISessionRepository
from src.domain.repositories.execution_repository import IExecutionRepository
from src.domain.repositories.template_repository import ITemplateRepository


class RepositoryFactory:
    """
    Repository factory

    Builds the repositories and injects the database session.
    """

    @staticmethod
    @asynccontextmanager
    async def get_repositories() -> AsyncGenerator[dict[str, object], None]:
        """
        Get every repository, as a context manager

        Usage:
            async with RepositoryFactory.get_repositories() as repos:
                session_repo = repos["session_repo"]
                # use the repositories
        """
        async with db_manager.get_session() as session:
            yield {
                "session_repo": SqlSessionRepository(session),
                "execution_repo": SqlExecutionRepository(session),
                "template_repo": SqlTemplateRepository(session),
            }

    @staticmethod
    def create_session_repository(session) -> ISessionRepository:
        """Build the session repository"""
        return SqlSessionRepository(session)

    @staticmethod
    def create_execution_repository(session) -> IExecutionRepository:
        """Build the execution repository"""
        return SqlExecutionRepository(session)

    @staticmethod
    def create_template_repository(session) -> ITemplateRepository:
        """Build the template repository"""
        return SqlTemplateRepository(session)
