from contextlib import asynccontextmanager

from langgraph.checkpoint.memory import MemorySaver

from app.config import config

_memory_saver = MemorySaver()


@asynccontextmanager
async def open_checkpointer():
    """checkpointer 后端：mysql（共享 openbkn 库）| memory（开发用，重启即丢）。

    表结构统一由 migrations/bkn-agent/ 建（core-data-migrator 执行），
    运行时不做 DDL；CHECKPOINTER_ALLOW_RUNTIME_DDL=true 仅供开发环境首启。
    """
    if config.CHECKPOINTER_BACKEND == "memory":
        yield _memory_saver
        return

    import aiomysql
    from langgraph.checkpoint.mysql.aio import AIOMySQLSaver

    # 不走 AIOMySQLSaver.from_conn_string：它丢弃 charset/collation，会话落到
    # server 默认（MariaDB 11 = utf8mb4_uca1400_ai_ci），与库表 utf8mb4_unicode_ci
    # 比较时报 1267 Illegal mix of collations。saver 的 SELECT 里 json_table(...
    # CHARACTER SET utf8mb4) 不带 COLLATE，取的是 server 端 charset 默认 collation，
    # 光 SET NAMES 管不住——用 character_set_collations（MariaDB 11.2+）把本会话
    # utf8mb4 的默认 collation 一并钉成 utf8mb4_unicode_ci。
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
