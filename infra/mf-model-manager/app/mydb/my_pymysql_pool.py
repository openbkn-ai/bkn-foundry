# -*-coding:utf-8-*-

from contextlib import contextmanager

from app.mydb.pymysql_pool import PymysqlPool


def connect_execute_commit_close_db(func):
    def wrapper(*args, **kwargs):
        pymysql_pool = PymysqlPool.get_pool()
        connection = pymysql_pool.connection()
        cursor = connection.cursor()
        kwargs['connection'] = connection
        kwargs['cursor'] = cursor
        try:
            ret = func(*args, **kwargs)
            connection.commit()
            return ret
        except Exception as e:
            connection.rollback()
            raise e
        finally:
            cursor.close()
            connection.close()
        return None

    return wrapper


def connect_execute_close_db(func):
    def wrapper(*args, **kwargs):
        pymysql_pool = PymysqlPool.get_pool()
        connection = pymysql_pool.connection()
        kwargs['connection'] = connection
        cursor = connection.cursor()
        kwargs['cursor'] = cursor
        try:
            ret = func(*args, **kwargs)
            return ret
        except Exception as e:
            raise e
        finally:
            cursor.close()
            connection.close()
        return None

    return wrapper


@contextmanager
def advisory_lock_transaction(lock_name, timeout=10):
    """Run a database transaction while holding a MariaDB advisory lock.

    Default-model selection is a read-modify-write operation.  Holding the
    connection-scoped lock until after ``commit`` serializes that operation
    without requiring a schema migration or leaving multiple defaults during
    concurrent model creation.
    """
    pymysql_pool = PymysqlPool.get_pool()
    connection = pymysql_pool.connection()
    cursor = connection.cursor()
    lock_acquired = False
    try:
        cursor.execute("SELECT GET_LOCK(%s, %s) AS lock_acquired", (lock_name, timeout))
        lock_result = cursor.fetchall()
        lock_acquired = bool(lock_result and lock_result[0].get("lock_acquired"))
        if not lock_acquired:
            raise RuntimeError(f"Timed out acquiring database lock: {lock_name}")
        yield cursor
        connection.commit()
    except Exception:
        connection.rollback()
        raise
    finally:
        if lock_acquired:
            try:
                cursor.execute("SELECT RELEASE_LOCK(%s)", (lock_name,))
            except Exception:
                # The transaction result is already known; closing the
                # connection also releases an advisory lock as a fallback.
                pass
        cursor.close()
        connection.close()
