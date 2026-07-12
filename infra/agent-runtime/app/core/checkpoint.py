from contextlib import asynccontextmanager

from langgraph.checkpoint.memory import MemorySaver

from app.config import config

_memory_saver = MemorySaver()


@asynccontextmanager
async def open_checkpointer():
    """checkpointer 后端：mysql（共享 openbkn 库）| memory（开发用，重启即丢）。

    表结构统一由 migrations/agent-runtime/ 建（core-data-migrator 执行），
    运行时不做 DDL；CHECKPOINTER_ALLOW_RUNTIME_DDL=true 仅供开发环境首启。
    """
    if config.CHECKPOINTER_BACKEND == "memory":
        yield _memory_saver
        return

    from langgraph.checkpoint.mysql.aio import AIOMySQLSaver

    async with AIOMySQLSaver.from_conn_string(config.checkpointer_conn) as saver:
        if config.CHECKPOINTER_ALLOW_RUNTIME_DDL:
            await saver.setup()
        yield saver
