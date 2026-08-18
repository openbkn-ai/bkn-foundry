"""
Database connection management

Configures and manages the SQLAlchemy async engine and sessions.
"""

from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import AsyncGenerator
from urllib.parse import urlparse, urlunparse

import aiomysql

from sqlalchemy import text
from sqlalchemy.ext.asyncio import (
    create_async_engine,
    async_sessionmaker,
    AsyncSession,
    AsyncEngine,
)
from sqlalchemy.orm import DeclarativeBase

from src.infrastructure.config.settings import get_settings
from src.infrastructure.logging import get_logger


class Base(DeclarativeBase):
    """SQLAlchemy declarative base"""

    pass


logger = get_logger(__name__)

LEGACY_DATABASE_NAME = "adp"
TARGET_DATABASE_NAME = "openbkn"


# Import all models so they're registered with Base.metadata
# This is required for create_all() to find all tables
from src.infrastructure.persistence.models.template_model import TemplateModel
from src.infrastructure.persistence.models.session_model import SessionModel
from src.infrastructure.persistence.models.execution_model import ExecutionModel
from src.infrastructure.persistence.models.runtime_node_model import RuntimeNodeModel


class DatabaseManager:
    """
    Database manager

    Creates and manages the database connections.
    """

    def __init__(self):
        self._engine: AsyncEngine | None = None
        self._session_factory: async_sessionmaker[AsyncSession] | None = None

    def _get_managed_sandbox_table_names(self) -> set[str]:
        """The allowlist of sandbox tables this control plane owns."""
        return set(Base.metadata.tables.keys())

    @dataclass(frozen=True)
    class _DatabaseConnectionInfo:
        """Database connection details."""

        user: str | None
        password: str | None
        host: str | None
        port: int
        database: str

    def _get_database_connection_info(self) -> _DatabaseConnectionInfo:
        """Resolve the connection details from the configuration."""
        parsed = urlparse(self._get_runtime_database_url())
        configured_database = parsed.path.lstrip("/")

        return self._DatabaseConnectionInfo(
            user=parsed.username,
            password=parsed.password,
            host=parsed.hostname,
            port=parsed.port or 3306,
            database=configured_database,
        )

    def _get_runtime_database_url(self) -> str:
        """Return the database URL used at runtime, normalizing the old name to the new one."""
        settings = get_settings()
        parsed = urlparse(settings.effective_database_url)
        database_name = parsed.path.lstrip("/")
        if database_name != LEGACY_DATABASE_NAME:
            return settings.effective_database_url

        normalized = parsed._replace(path=f"/{TARGET_DATABASE_NAME}")
        return urlunparse(normalized)

    async def _create_server_pool(self) -> aiomysql.Pool:
        """Create the connection pool to the MySQL server."""
        connection_info = self._get_database_connection_info()
        return await aiomysql.create_pool(
            host=connection_info.host,
            port=connection_info.port,
            user=connection_info.user,
            password=connection_info.password,
            db=None,
            autocommit=True,
            minsize=1,
            maxsize=1,
        )

    async def upgrade_legacy_database_name(self) -> None:
        """
        Migrate the old database name to the new one at start-up.

        Currently this upgrades the old `adp` database to `openbkn`.
        """
        connection_info = self._get_database_connection_info()
        target_database = connection_info.database
        if target_database != TARGET_DATABASE_NAME:
            logger.info(
                "Skipping legacy database rename because configured target database is not managed",
                database=target_database,
            )
            return

        pool = await self._create_server_pool()
        try:
            async with pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    legacy_exists = await self._schema_exists(cursor, LEGACY_DATABASE_NAME)
                    if not legacy_exists:
                        logger.debug(
                            "Legacy database not found, skipping rename",
                            legacy_database=LEGACY_DATABASE_NAME,
                        )
                        return

                    target_exists = await self._schema_exists(cursor, TARGET_DATABASE_NAME)
                    if target_exists:
                        logger.warning(
                            "Target database already exists, checking for missing legacy tables",
                            legacy_database=LEGACY_DATABASE_NAME,
                            target_database=TARGET_DATABASE_NAME,
                        )
                    else:
                        logger.info(
                            "Migrating legacy database name",
                            legacy_database=LEGACY_DATABASE_NAME,
                            target_database=TARGET_DATABASE_NAME,
                        )
                        await cursor.execute(
                            f"CREATE DATABASE `{TARGET_DATABASE_NAME}` "
                            "CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
                        )

                    managed_table_names = self._get_managed_sandbox_table_names()
                    legacy_table_names = [
                        table_name
                        for table_name in await self._list_tables(cursor, LEGACY_DATABASE_NAME)
                        if table_name in managed_table_names
                    ]
                    target_table_names = [
                        table_name
                        for table_name in await self._list_tables(cursor, TARGET_DATABASE_NAME)
                        if table_name in managed_table_names
                    ]
                    missing_table_names = [
                        table_name
                        for table_name in legacy_table_names
                        if table_name not in set(target_table_names)
                    ]

                    for table_name in missing_table_names:
                        await cursor.execute(
                            f"RENAME TABLE `{LEGACY_DATABASE_NAME}`.`{table_name}` "
                            f"TO `{TARGET_DATABASE_NAME}`.`{table_name}`"
                        )

                    remaining_legacy_tables = [
                        table_name
                        for table_name in await self._list_tables(cursor, LEGACY_DATABASE_NAME)
                        if table_name in managed_table_names
                    ]
                    if remaining_legacy_tables:
                        logger.warning(
                            "Legacy database still contains tables after migration",
                            legacy_database=LEGACY_DATABASE_NAME,
                            target_database=TARGET_DATABASE_NAME,
                            migrated_tables=len(missing_table_names),
                            remaining_tables=remaining_legacy_tables,
                        )
                        return

                    logger.info(
                        "Legacy database migration completed without dropping source database",
                        legacy_database=LEGACY_DATABASE_NAME,
                        target_database=TARGET_DATABASE_NAME,
                        migrated_tables=len(missing_table_names),
                    )
        finally:
            pool.close()
            await pool.wait_closed()

    async def _schema_exists(self, cursor, schema_name: str) -> bool:
        """Check whether the schema exists."""
        await cursor.execute(
            """
            SELECT COUNT(*)
            FROM INFORMATION_SCHEMA.SCHEMATA
            WHERE SCHEMA_NAME = %s
            """,
            (schema_name,),
        )
        result = await cursor.fetchone()
        return bool(result and result[0])

    async def _list_tables(self, cursor, schema_name: str) -> list[str]:
        """List every base table in the schema."""
        await cursor.execute(
            """
            SELECT TABLE_NAME
            FROM INFORMATION_SCHEMA.TABLES
            WHERE TABLE_SCHEMA = %s
              AND TABLE_TYPE = 'BASE TABLE'
            ORDER BY TABLE_NAME
            """,
            (schema_name,),
        )
        rows = await cursor.fetchall()
        return [row[0] for row in rows]

    async def ensure_database_exists(self) -> None:
        """
        Make sure the database exists, creating it when it does not

        Uses a raw connection rather than SQLAlchemy, because SQLAlchemy needs the
        database to exist before it can build an engine.
        """
        connection_info = self._get_database_connection_info()
        db_name = connection_info.database
        pool = await self._create_server_pool()

        try:
            async with pool.acquire() as conn:
                async with conn.cursor() as cursor:
                    # Check whether the database exists
                    result = await self._schema_exists(cursor, db_name)

                    if not result:
                        # It does not, so create it
                        await cursor.execute(
                            f"CREATE DATABASE `{db_name}` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"
                        )
                        logger.info(f"Database '{db_name}' created successfully")
                    else:
                        logger.debug(f"Database '{db_name}' already exists")
        finally:
            pool.close()
            await pool.wait_closed()

    async def initialize(self) -> None:
        """Initialize the database engine, making sure the database exists"""
        # Make sure the database exists first
        await self.ensure_database_exists()

        # Then build the engine
        settings = get_settings()
        self._engine = create_async_engine(
            self._get_runtime_database_url(),
            echo=settings.log_level == "DEBUG",
            pool_size=settings.db_pool_size,
            max_overflow=settings.db_max_overflow,
            pool_recycle=settings.db_pool_recycle,
        )
        self._session_factory = async_sessionmaker(
            bind=self._engine,
            class_=AsyncSession,
            expire_on_commit=False,
        )

    async def create_tables(self) -> None:
        """Create every database table"""
        if self._engine is None:
            raise RuntimeError("DatabaseManager not initialized. Call initialize() first.")

        async with self._engine.begin() as conn:
            await conn.run_sync(Base.metadata.create_all)

    async def run_startup_schema_migrations(self) -> None:
        """
        Run an idempotent schema upgrade at start-up.

        Currently this covers an older database missing the `f_python_package_index_url` column.
        """
        if self._engine is None:
            raise RuntimeError("DatabaseManager not initialized. Call initialize() first.")

        backend_name = self._engine.url.get_backend_name()
        if backend_name != "mysql":
            logger.info(
                "Skipping startup schema migrations for unsupported backend",
                backend=backend_name,
            )
            return

        async with self._engine.begin() as conn:
            table_name = "t_sandbox_session"
            column_name = "f_python_package_index_url"

            table_exists = await self._mariadb_table_exists(conn, table_name)
            if not table_exists:
                logger.info(
                    "Skipping startup schema migration because target table does not exist",
                    table=table_name,
                )
                return

            column_exists = await self._mariadb_column_exists(
                conn,
                table_name,
                column_name,
            )
            if column_exists:
                logger.info(
                    "Startup schema migration check passed",
                    table=table_name,
                    column=column_name,
                    action="skip",
                )
                return

            logger.info(
                "Applying startup schema migration",
                table=table_name,
                column=column_name,
                action="add_column",
            )
            await conn.execute(text("""
                    ALTER TABLE `t_sandbox_session`
                    ADD COLUMN `f_python_package_index_url` varchar(512) NOT NULL
                    DEFAULT 'https://pypi.org/simple/'
                    AFTER `f_completed_at`
                    """))
            logger.info(
                "Startup schema migration applied successfully",
                table=table_name,
                column=column_name,
            )

    async def _mariadb_table_exists(self, conn, table_name: str) -> bool:
        """Check whether a MariaDB table exists."""
        result = await conn.execute(
            text("""
                SELECT COUNT(*)
                FROM information_schema.TABLES
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = :table_name
                """),
            {"table_name": table_name},
        )
        return bool(result.scalar())

    async def _mariadb_column_exists(self, conn, table_name: str, column_name: str) -> bool:
        """Check whether a MariaDB column exists."""
        result = await conn.execute(
            text("""
                SELECT COUNT(*)
                FROM information_schema.COLUMNS
                WHERE TABLE_SCHEMA = DATABASE()
                  AND TABLE_NAME = :table_name
                  AND COLUMN_NAME = :column_name
                """),
            {"table_name": table_name, "column_name": column_name},
        )
        return bool(result.scalar())

    async def initialize_with_seed(
        self, create_tables: bool = False, seed_data: bool = False, force_seed: bool = False
    ) -> dict:
        """
        Initialize the database, optionally creating the tables and the seed data

        Args:
            create_tables: whether to create the database tables
            seed_data: whether to insert the seed data
            force_seed: whether to recreate the seed data

        Returns:
            A dict holding the initialization result
        """
        result = {"tables_created": False, "seeded": False, "seed_stats": {}}

        if create_tables:
            await self.create_tables()
            result["tables_created"] = True

        if seed_data:
            from src.infrastructure.persistence.seed.seeder import seed_default_data

            stats = await seed_default_data(force=force_seed)
            result["seeded"] = True
            result["seed_stats"] = stats

        return result

    @asynccontextmanager
    async def get_session(self) -> AsyncGenerator[AsyncSession, None]:
        """
        Get a database session, as a context manager

        Usage:
            async with db_manager.get_session() as session:
                # use the session
        """
        if self._session_factory is None:
            raise RuntimeError("DatabaseManager not initialized. Call initialize() first.")

        async with self._session_factory() as session:
            try:
                yield session
                await session.commit()
            except Exception:
                await session.rollback()
                raise

    async def close(self) -> None:
        """Close the database connections"""
        if self._engine:
            await self._engine.dispose()


# The global database manager instance
db_manager = DatabaseManager()
