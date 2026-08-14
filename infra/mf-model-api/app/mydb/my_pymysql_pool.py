# -*-coding:utf-8-*-

import functools

from app.mydb.pymysql_pool import PymysqlPool


def connect_execute_commit_close_db(func):
    # functools.wraps 保留被包函数的名字/文档，并留下 __wrapped__——没有它，dao 层
    # 的 SQL 就只能连着真库才验得到，单测里拿不到未包装的函数体。
    @functools.wraps(func)
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
    @functools.wraps(func)
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
