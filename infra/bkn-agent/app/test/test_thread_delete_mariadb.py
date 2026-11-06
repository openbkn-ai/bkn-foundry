# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Deleting a thread on the MySQL checkpointer, against the migration's tables.

Only a disposable random database on an isolated MariaDB is touched.
"""
import asyncio
import os
import re
import uuid
from pathlib import Path

import pymysql
import pytest
from langchain_core.messages import AIMessage
from langgraph.graph import END, START, MessagesState, StateGraph

from app.config import config
from app.core import graph as agent_graph
from app.core.checkpoint import open_checkpointer

MIGRATIONS = Path(__file__).resolve().parents[4] / "migrations" / "bkn-agent" / "mariadb"


def _latest_init_sql() -> str:
    versions = sorted(
        (path for path in MIGRATIONS.iterdir() if (path / "init.sql").exists()),
        key=lambda path: tuple(int(part) for part in path.name.split(".")),
    )
    return (versions[-1] / "init.sql").read_text(encoding="utf-8")


def _checkpoint_ddl() -> list[str]:
    statements = re.findall(
        r"create table if not exists checkpoint\w*\s*\(.*?\)[^;]*;",
        _latest_init_sql(),
        flags=re.S | re.I,
    )
    assert len(statements) == 4, statements
    return statements


def _answer(state: MessagesState):
    return {"messages": [AIMessage(content="ok")]}


async def _write_turn(thread_id: str) -> None:
    builder = StateGraph(MessagesState)
    builder.add_node("answer", _answer)
    builder.add_edge(START, "answer")
    builder.add_edge("answer", END)
    async with open_checkpointer() as checkpointer:
        compiled = builder.compile(checkpointer=checkpointer)
        await compiled.ainvoke(
            {"messages": [("user", "hi")]},
            {"configurable": {"thread_id": thread_id}},
        )


def _count(db, table: str, thread_id: str) -> int:
    with db.cursor() as cursor:
        cursor.execute(f"SELECT COUNT(*) FROM {table} WHERE thread_id = %s", (thread_id,))
        return cursor.fetchone()[0]


@pytest.mark.skipif(not os.getenv("BKN_AGENT_TEST_MARIADB_HOST"), reason="isolated MariaDB not configured")
def test_delete_thread_checkpoints_on_mariadb(monkeypatch):
    host = os.environ["BKN_AGENT_TEST_MARIADB_HOST"]
    port = int(os.getenv("BKN_AGENT_TEST_MARIADB_PORT", "3306"))
    name = "thread_delete_" + uuid.uuid4().hex
    admin = pymysql.connect(host=host, port=port, user="root", password="", autocommit=True)
    with admin.cursor() as cursor:
        cursor.execute(f"CREATE DATABASE `{name}`")
    db = pymysql.connect(host=host, port=port, user="root", password="", database=name, autocommit=True)
    try:
        with db.cursor() as cursor:
            for statement in _checkpoint_ddl():
                cursor.execute(statement)
        monkeypatch.setattr(config, "CHECKPOINTER_BACKEND", "mysql")
        monkeypatch.setattr(config, "CHECKPOINTER_ALLOW_RUNTIME_DDL", False)
        monkeypatch.setattr(config, "RDS_HOST", host)
        monkeypatch.setattr(config, "RDS_PORT", port)
        monkeypatch.setattr(config, "RDS_USER", "root")
        monkeypatch.setattr(config, "RDS_PASS", "")
        monkeypatch.setattr(config, "RDS_DBNAME", name)

        asyncio.run(_write_turn("t-delete"))
        asyncio.run(_write_turn("t-keep"))
        for table in ("checkpoints", "checkpoint_blobs", "checkpoint_writes"):
            assert _count(db, table, "t-delete") > 0, table

        asyncio.run(agent_graph.delete_thread_checkpoints("t-delete"))

        for table in ("checkpoints", "checkpoint_blobs", "checkpoint_writes"):
            assert _count(db, table, "t-delete") == 0, table
        assert _count(db, "checkpoints", "t-keep") > 0
        assert asyncio.run(agent_graph.read_thread_messages("t-delete")) == []
        assert [m.role for m in asyncio.run(agent_graph.read_thread_messages("t-keep"))] == ["user", "assistant"]
    finally:
        db.close()
        with admin.cursor() as cursor:
            cursor.execute(f"DROP DATABASE `{name}`")
        admin.close()
