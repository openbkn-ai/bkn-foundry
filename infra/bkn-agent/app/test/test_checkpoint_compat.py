import aiomysql.connection
from pymysql import converters

from app.core import checkpoint


def test_checkpoint_restores_callable_bytes_escape_when_pymysql_exports_sentinel(monkeypatch):
    monkeypatch.setattr(
        aiomysql.connection, "escape_bytes_prefixed", "DO NOT IMPORT THIS!!!"
    )

    ensure_safe_escape = getattr(checkpoint, "_ensure_safe_aiomysql_bytes_escape", None)
    assert callable(ensure_safe_escape)
    ensure_safe_escape()

    assert aiomysql.connection.escape_bytes_prefixed is converters.escape_bytes
    connection = object.__new__(aiomysql.connection.Connection)
    connection._writer = None
    assert connection.escape(b"\x00\xff") == converters.escape_bytes(b"\x00\xff")


def test_checkpoint_keeps_existing_callable_bytes_escape(monkeypatch):
    original = lambda value: "escaped"  # noqa: E731 - exact identity is the behavior
    monkeypatch.setattr(aiomysql.connection, "escape_bytes_prefixed", original)

    ensure_safe_escape = getattr(checkpoint, "_ensure_safe_aiomysql_bytes_escape", None)
    assert callable(ensure_safe_escape)
    ensure_safe_escape()

    assert aiomysql.connection.escape_bytes_prefixed is original
