from unittest import TestCase, mock
from app.dao.small_model_dao import small_model_dao
from app.interfaces.dbaccess import AddExternalSmallModelInfo
from app.mydb.pymysql_pool import PymysqlPool
from app.logs.stand_log import StandLogger


def _mock_pool():
    """构造 connect_execute_*_close_db 装饰器所需的连接链 mock。
    装饰器内部: PymysqlPool.get_pool() -> pool.connection() -> conn.cursor()。
    返回 cursor mock 以便单测设置 fetchall。"""
    pool = mock.MagicMock()
    conn = mock.MagicMock()
    cursor = mock.MagicMock()
    pool.connection.return_value = conn
    conn.cursor.return_value = cursor
    cursor.execute.return_value = True
    cursor.fetchall.return_value = "test"
    cursor.lastrowid = 1
    PymysqlPool.get_pool = mock.Mock(return_value=pool)
    return cursor


def _config_info():
    return AddExternalSmallModelInfo(
        model_id="1",
        model_name="1",
        model_type="1",
        model_config={},
    )


class TestAddModelInfo(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_add_model_info_success(self):
        _mock_pool()
        # connection/cursor 由装饰器注入；公开调用只传 config_info + userId
        res = small_model_dao.add_model_info(_config_info(), "user1")
        self.assertEqual(res, None)


class TestEditModelInfo(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_edit_model_info_success(self):
        _mock_pool()
        res = small_model_dao.edit_model_info(_config_info(), "user1")
        self.assertEqual(res, None)


class TestGetModelInfoById(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_get_model_info_by_id_success(self):
        _mock_pool()
        res = small_model_dao.get_model_info_by_id("!")
        self.assertEqual(res, "test")


class TestGetModelInfoByName(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_get_model_info_by_name_success(self):
        _mock_pool()
        res = small_model_dao.get_model_info_by_name("!")
        self.assertEqual(res, "test")


class TestGetModelInfoList(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_get_model_info_list_success(self):
        _mock_pool()
        # 签名: page, size, order, rule, model_name, model_type, model_series, permission_ids
        res = small_model_dao.get_model_info_list(1, 10, "asc", "update_time", "", "embedding", "baidu", [])
        self.assertEqual(res, "test")


class TestDeleteModelInfoByIds(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_delete_model_info_by_ids_success(self):
        _mock_pool()
        res = small_model_dao.delete_model_info_by_ids(["1"])
        self.assertEqual(res, None)


class TestNameCheck(TestCase):
    def setUp(self) -> None:
        self.mysqlPool = PymysqlPool

    def tearDown(self) -> None:
        PymysqlPool = self.mysqlPool
        StandLogger.stand_log_shutdown()

    def test_name_check_success(self):
        _mock_pool()
        res = small_model_dao.name_check("1")
        self.assertEqual(res, "test")


if __name__ == '__main__':
    import unittest

    unittest.main()
