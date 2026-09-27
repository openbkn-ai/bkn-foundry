from contextlib import asynccontextmanager

from langgraph.checkpoint.memory import MemorySaver

from app.config import config

_memory_saver = MemorySaver()


def _ensure_safe_aiomysql_bytes_escape() -> None:
    """PyMySQL 1.2.3 exports a non-callable legacy symbol used by aiomysql 0.2."""
    import aiomysql.connection
    from pymysql.converters import escape_bytes

    if not callable(getattr(aiomysql.connection, "escape_bytes_prefixed", None)):
        aiomysql.connection.escape_bytes_prefixed = escape_bytes


@asynccontextmanager
async def open_checkpointer():
    """Checkpointer backend: mysql (the shared openbkn database) or memory (for
    development, discarded on restart).

    The tables are created solely by migrations/bkn-agent/, executed by
    core-data-migrator; no DDL runs at runtime.
    CHECKPOINTER_ALLOW_RUNTIME_DDL=true exists only for the first start of a
    development environment.
    """
    if config.CHECKPOINTER_BACKEND == "memory":
        yield _memory_saver
        return

    import aiomysql
    from langgraph.checkpoint.mysql.aio import AIOMySQLSaver

    _ensure_safe_aiomysql_bytes_escape()

    # Not AIOMySQLSaver.from_conn_string: it drops charset/collation, so the
    # session falls back to the server default (MariaDB 11 =
    # utf8mb4_uca1400_ai_ci) and comparing that against the table's
    # utf8mb4_unicode_ci raises 1267 Illegal mix of collations. The saver's
    # SELECT uses json_table(... CHARACTER SET utf8mb4) without COLLATE, which
    # picks the server-side default collation for that charset, and SET NAMES
    # alone does not cover it. character_set_collations (MariaDB 11.2+) pins the
    # session default for utf8mb4 to utf8mb4_unicode_ci as well.
    async with aiomysql.connect(
        host=config.RDS_HOST,
        port=config.RDS_PORT,
        user=config.RDS_USER,
        password=config.RDS_PASS,
        db=config.RDS_DBNAME,
        autocommit=True,
        charset="utf8mb4",
        init_command=(
            "SET SESSION character_set_collations='utf8mb4=utf8mb4_unicode_ci', "
            "collation_connection='utf8mb4_unicode_ci'"
        ),
    ) as conn:
        saver = AIOMySQLSaver(conn=conn)
        if config.CHECKPOINTER_ALLOW_RUNTIME_DDL:
            await saver.setup()
        yield saver
