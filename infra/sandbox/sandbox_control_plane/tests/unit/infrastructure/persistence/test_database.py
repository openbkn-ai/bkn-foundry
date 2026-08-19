"""Unit tests for database."""
import pytest
from unittest.mock import Mock, AsyncMock, patch

from src.infrastructure.persistence.database import (
    Base,
    DatabaseManager,
    LEGACY_DATABASE_NAME,
    TARGET_DATABASE_NAME,
)


class TestDatabaseManager:
    """Tests for TestDatabaseManager."""

    @pytest.fixture
    def mock_settings(self):
        """Create settings."""
        settings = Mock()
        settings.effective_database_url = "mysql+aiomysql://root:password@localhost:3306/openbkn"
        settings.log_level = "INFO"
        settings.db_pool_size = 5
        settings.db_max_overflow = 10
        settings.db_pool_recycle = 3600
        return settings

    @pytest.fixture
    def db_manager(self):
        """Create db manager."""
        return DatabaseManager()

    def test_init(self, db_manager):
        """Test init."""
        assert db_manager._engine is None
        assert db_manager._session_factory is None

    def test_get_runtime_database_url_normalizes_legacy_database_name(self, db_manager, mock_settings):
        """Test get runtime database URL normalizes legacy database name."""
        mock_settings.effective_database_url = "mysql+aiomysql://root:password@localhost:3306/adp"

        with patch("src.infrastructure.persistence.database.get_settings", return_value=mock_settings):
            runtime_url = db_manager._get_runtime_database_url()

        assert runtime_url == "mysql+aiomysql://root:password@localhost:3306/openbkn"

    def test_get_managed_sandbox_table_names(self, db_manager):
        """Test get managed sandbox table names."""
        assert db_manager._get_managed_sandbox_table_names() == {
            "t_sandbox_execution",
            "t_sandbox_runtime_node",
            "t_sandbox_session",
            "t_sandbox_template",
        }

    @pytest.mark.asyncio
    async def test_ensure_database_exists_integration(self, db_manager):
        """Test ensure database exists integration."""
        # This test requires actual database connection
        # Skip in unit tests
        pass

    @pytest.mark.asyncio
    async def test_initialize_integration(self, db_manager):
        """Test initialize integration."""
        # This test requires actual database connection
        # Skip in unit tests
        pass

    @pytest.mark.asyncio
    async def test_create_tables_not_initialized(self, db_manager):
        """Test create tables not initialized."""
        with pytest.raises(RuntimeError, match="not initialized"):
            await db_manager.create_tables()

    @pytest.mark.asyncio
    async def test_run_startup_schema_migrations_not_initialized(self, db_manager):
        """Test run startup schema migrations not initialized."""
        with pytest.raises(RuntimeError, match="not initialized"):
            await db_manager.run_startup_schema_migrations()

    @pytest.mark.asyncio
    async def test_initialize_with_seed_no_tables_no_seed(self, db_manager):
        """Test initialize with seed no tables no seed."""
        result = await db_manager.initialize_with_seed(
            create_tables=False,
            seed_data=False
        )

        assert result["tables_created"] is False
        assert result["seeded"] is False
        assert result["seed_stats"] == {}

    @pytest.mark.asyncio
    async def test_initialize_with_seed_create_tables(self, db_manager):
        """Test initialize with seed create tables."""
        with patch.object(db_manager, 'create_tables', new_callable=AsyncMock):
            result = await db_manager.initialize_with_seed(
                create_tables=True,
                seed_data=False
            )

            assert result["tables_created"] is True
            assert result["seeded"] is False

    @pytest.mark.asyncio
    async def test_initialize_with_seed_with_seed(self, db_manager):
        """Test initialize with seed with seed."""
        with patch.object(db_manager, 'create_tables', new_callable=AsyncMock):
            with patch('src.infrastructure.persistence.seed.seeder.seed_default_data',
                      new_callable=AsyncMock, return_value={"templates": 1, "runtime_nodes": 1}):
                result = await db_manager.initialize_with_seed(
                    create_tables=True,
                    seed_data=True
                )

                assert result["tables_created"] is True
                assert result["seeded"] is True
                assert result["seed_stats"]["templates"] == 1

    @pytest.mark.asyncio
    async def test_get_session_not_initialized(self, db_manager):
        """Test get session not initialized."""
        with pytest.raises(RuntimeError, match="not initialized"):
            async with db_manager.get_session():
                pass

    @pytest.mark.asyncio
    async def test_close_no_engine(self, db_manager):
        """Test close no engine."""
        # Should not raise error
        await db_manager.close()

    @pytest.mark.asyncio
    async def test_close_with_engine(self, db_manager):
        """Test close with engine."""
        mock_engine = Mock()
        mock_engine.dispose = AsyncMock()
        db_manager._engine = mock_engine

        await db_manager.close()

        mock_engine.dispose.assert_called_once()

    @pytest.mark.asyncio
    async def test_upgrade_legacy_database_name_migrates_managed_tables_without_dropping_legacy_database(
        self,
        db_manager,
        mock_settings,
    ):
        """Test upgrade legacy database name migrates managed tables without dropping legacy database."""
        mock_cursor = AsyncMock()
        mock_cursor.fetchone = AsyncMock(side_effect=[(1,), (0,)])
        mock_cursor.fetchall = AsyncMock(
            side_effect=[
                [("t_sandbox_session",), ("t_sandbox_execution",)],
                [],
                [],
            ]
        )

        mock_cursor_context = AsyncMock()
        mock_cursor_context.__aenter__.return_value = mock_cursor
        mock_cursor_context.__aexit__.return_value = None

        mock_conn = Mock()
        mock_conn.cursor = Mock(return_value=mock_cursor_context)

        mock_acquire = AsyncMock()
        mock_acquire.__aenter__.return_value = mock_conn
        mock_acquire.__aexit__.return_value = None

        mock_pool = Mock()
        mock_pool.acquire = Mock(return_value=mock_acquire)
        mock_pool.close = Mock()
        mock_pool.wait_closed = AsyncMock()

        with patch("src.infrastructure.persistence.database.get_settings", return_value=mock_settings):
            with patch(
                "src.infrastructure.persistence.database.aiomysql.create_pool",
                AsyncMock(return_value=mock_pool),
            ):
                await db_manager.upgrade_legacy_database_name()

        executed_sql = [call.args[0] for call in mock_cursor.execute.await_args_list]
        assert any(f"CREATE DATABASE `{TARGET_DATABASE_NAME}`" in stmt for stmt in executed_sql)
        assert any(
            f"RENAME TABLE `{LEGACY_DATABASE_NAME}`.`t_sandbox_session` "
            f"TO `{TARGET_DATABASE_NAME}`.`t_sandbox_session`" in stmt
            for stmt in executed_sql
        )
        assert any(
            f"RENAME TABLE `{LEGACY_DATABASE_NAME}`.`t_sandbox_execution` "
            f"TO `{TARGET_DATABASE_NAME}`.`t_sandbox_execution`" in stmt
            for stmt in executed_sql
        )
        assert not any(f"DROP DATABASE `{LEGACY_DATABASE_NAME}`" in stmt for stmt in executed_sql)

    @pytest.mark.asyncio
    async def test_upgrade_legacy_database_name_migrates_missing_tables_when_target_exists(
        self,
        db_manager,
        mock_settings,
    ):
        """Test upgrade legacy database name migrates missing tables when target exists."""
        mock_cursor = AsyncMock()
        mock_cursor.fetchone = AsyncMock(side_effect=[(1,), (1,)])
        mock_cursor.fetchall = AsyncMock(
            side_effect=[
                [("t_sandbox_session",), ("t_sandbox_execution",)],
                [],
                [],
            ]
        )

        mock_cursor_context = AsyncMock()
        mock_cursor_context.__aenter__.return_value = mock_cursor
        mock_cursor_context.__aexit__.return_value = None

        mock_conn = Mock()
        mock_conn.cursor = Mock(return_value=mock_cursor_context)

        mock_acquire = AsyncMock()
        mock_acquire.__aenter__.return_value = mock_conn
        mock_acquire.__aexit__.return_value = None

        mock_pool = Mock()
        mock_pool.acquire = Mock(return_value=mock_acquire)
        mock_pool.close = Mock()
        mock_pool.wait_closed = AsyncMock()

        with patch("src.infrastructure.persistence.database.get_settings", return_value=mock_settings):
            with patch(
                "src.infrastructure.persistence.database.aiomysql.create_pool",
                AsyncMock(return_value=mock_pool),
            ):
                await db_manager.upgrade_legacy_database_name()

        executed_sql = [call.args[0] for call in mock_cursor.execute.await_args_list]
        assert not any(f"CREATE DATABASE `{TARGET_DATABASE_NAME}`" in stmt for stmt in executed_sql)
        assert any(
            f"RENAME TABLE `{LEGACY_DATABASE_NAME}`.`t_sandbox_execution` "
            f"TO `{TARGET_DATABASE_NAME}`.`t_sandbox_execution`" in stmt
            for stmt in executed_sql
        )
        assert not any(f"DROP DATABASE `{LEGACY_DATABASE_NAME}`" in stmt for stmt in executed_sql)

    @pytest.mark.asyncio
    async def test_upgrade_legacy_database_name_keeps_legacy_database_when_tables_remain(
        self,
        db_manager,
        mock_settings,
    ):
        """Test upgrade legacy database name keeps legacy database when tables remain."""
        mock_cursor = AsyncMock()
        mock_cursor.fetchone = AsyncMock(side_effect=[(1,), (1,)])
        mock_cursor.fetchall = AsyncMock(
            side_effect=[
                [("t_sandbox_session",), ("t_sandbox_execution",)],
                [("t_sandbox_session",), ("t_sandbox_execution",)],
                [("t_sandbox_session",), ("t_sandbox_execution",)],
            ]
        )

        mock_cursor_context = AsyncMock()
        mock_cursor_context.__aenter__.return_value = mock_cursor
        mock_cursor_context.__aexit__.return_value = None

        mock_conn = Mock()
        mock_conn.cursor = Mock(return_value=mock_cursor_context)

        mock_acquire = AsyncMock()
        mock_acquire.__aenter__.return_value = mock_conn
        mock_acquire.__aexit__.return_value = None

        mock_pool = Mock()
        mock_pool.acquire = Mock(return_value=mock_acquire)
        mock_pool.close = Mock()
        mock_pool.wait_closed = AsyncMock()

        with patch("src.infrastructure.persistence.database.get_settings", return_value=mock_settings):
            with patch(
                "src.infrastructure.persistence.database.aiomysql.create_pool",
                AsyncMock(return_value=mock_pool),
            ):
                await db_manager.upgrade_legacy_database_name()

        executed_sql = [call.args[0] for call in mock_cursor.execute.await_args_list]
        assert not any("RENAME TABLE" in stmt for stmt in executed_sql)
        assert not any(f"DROP DATABASE `{LEGACY_DATABASE_NAME}`" in stmt for stmt in executed_sql)

    @pytest.mark.asyncio
    async def test_upgrade_legacy_database_name_ignores_non_sandbox_tables(
        self,
        db_manager,
        mock_settings,
    ):
        """Test upgrade legacy database name ignores non sandbox tables."""
        mock_cursor = AsyncMock()
        mock_cursor.fetchone = AsyncMock(side_effect=[(1,), (1,)])
        mock_cursor.fetchall = AsyncMock(
            side_effect=[
                [("t_sandbox_session",), ("t_other_business",)],
                [],
                [],
            ]
        )

        mock_cursor_context = AsyncMock()
        mock_cursor_context.__aenter__.return_value = mock_cursor
        mock_cursor_context.__aexit__.return_value = None

        mock_conn = Mock()
        mock_conn.cursor = Mock(return_value=mock_cursor_context)

        mock_acquire = AsyncMock()
        mock_acquire.__aenter__.return_value = mock_conn
        mock_acquire.__aexit__.return_value = None

        mock_pool = Mock()
        mock_pool.acquire = Mock(return_value=mock_acquire)
        mock_pool.close = Mock()
        mock_pool.wait_closed = AsyncMock()

        with patch("src.infrastructure.persistence.database.get_settings", return_value=mock_settings):
            with patch(
                "src.infrastructure.persistence.database.aiomysql.create_pool",
                AsyncMock(return_value=mock_pool),
            ):
                await db_manager.upgrade_legacy_database_name()

        executed_sql = [call.args[0] for call in mock_cursor.execute.await_args_list]
        assert any(
            f"RENAME TABLE `{LEGACY_DATABASE_NAME}`.`t_sandbox_session` "
            f"TO `{TARGET_DATABASE_NAME}`.`t_sandbox_session`" in stmt
            for stmt in executed_sql
        )
        assert not any("t_other_business" in stmt for stmt in executed_sql)

    @pytest.mark.asyncio
    async def test_run_startup_schema_migrations_adds_missing_column(self, db_manager):
        """Test run startup schema migrations adds missing column."""
        mock_conn = AsyncMock()
        table_exists_result = Mock()
        table_exists_result.scalar.return_value = 1
        column_exists_result = Mock()
        column_exists_result.scalar.return_value = 0
        alter_result = Mock()
        mock_conn.execute = AsyncMock(
            side_effect=[table_exists_result, column_exists_result, alter_result]
        )

        mock_begin = AsyncMock()
        mock_begin.__aenter__.return_value = mock_conn
        mock_begin.__aexit__.return_value = None

        mock_engine = Mock()
        mock_engine.url.get_backend_name.return_value = "mysql"
        mock_engine.begin.return_value = mock_begin
        db_manager._engine = mock_engine

        await db_manager.run_startup_schema_migrations()

        assert mock_conn.execute.await_count == 3
        alter_stmt = str(mock_conn.execute.await_args_list[2].args[0])
        assert "ALTER TABLE `t_sandbox_session`" in alter_stmt
        assert "ADD COLUMN `f_python_package_index_url`" in alter_stmt

    @pytest.mark.asyncio
    async def test_run_startup_schema_migrations_skips_existing_column(self, db_manager):
        """Test run startup schema migrations skips existing column."""
        mock_conn = AsyncMock()
        table_exists_result = Mock()
        table_exists_result.scalar.return_value = 1
        column_exists_result = Mock()
        column_exists_result.scalar.return_value = 1
        mock_conn.execute = AsyncMock(
            side_effect=[table_exists_result, column_exists_result]
        )

        mock_begin = AsyncMock()
        mock_begin.__aenter__.return_value = mock_conn
        mock_begin.__aexit__.return_value = None

        mock_engine = Mock()
        mock_engine.url.get_backend_name.return_value = "mysql"
        mock_engine.begin.return_value = mock_begin
        db_manager._engine = mock_engine

        await db_manager.run_startup_schema_migrations()

        assert mock_conn.execute.await_count == 2


class TestBase:
    """Tests for TestBase."""

    def test_base_is_declarative_base(self):
        """Test base is declarative base."""
        from sqlalchemy.orm import DeclarativeBase
        assert issubclass(Base, DeclarativeBase)
