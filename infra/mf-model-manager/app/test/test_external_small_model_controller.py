from datetime import datetime
from unittest import TestCase, mock

from app.controller import small_model_controller
from app.dao.small_model_dao import small_model_dao
from app.interfaces import logics
from app.logs.stand_log import StandLogger
import asyncio
import json


class TestAddModel(TestCase):
    def setUp(self) -> None:
        self.name_check = small_model_dao.name_check
        self.add_model_info = small_model_dao.add_model_info

    def tearDown(self) -> None:
        small_model_dao.name_check = self.name_check
        small_model_dao.add_model_info = self.add_model_info
        StandLogger.stand_log_shutdown()

    def test_add_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        small_model_dao.name_check = mock.Mock(return_value=[])
        small_model_dao.add_model_info = mock.Mock(return_value=None)
        para = logics.AddExternalSmallModel(
            model_name="1",
            model_type="embedding",
            model_config={"api_url": "http://x", "api_model": "m"},
            batch_size=16,
            max_tokens=512,
            embedding_dim=768,
        )
        res = loop.run_until_complete(
            small_model_controller.add_model(para, "1", "zh", "user"))
        self.assertEqual(isinstance(json.loads(res.body)["id"], str), True)


class TestEditModel(TestCase):
    def setUp(self) -> None:
        self.name_check = small_model_dao.name_check
        self.edit_model_info = small_model_dao.edit_model_info
        self.get_model_info_by_id = small_model_dao.get_model_info_by_id
        self.redis_util = small_model_controller.redis_util

    def tearDown(self) -> None:
        small_model_dao.name_check = self.name_check
        small_model_dao.edit_model_info = self.edit_model_info
        small_model_dao.get_model_info_by_id = self.get_model_info_by_id
        small_model_controller.redis_util = self.redis_util
        StandLogger.stand_log_shutdown()

    def test_edit_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        small_model_dao.get_model_info_by_id = mock.Mock(return_value=[{
            "f_model_id": "1234567890987654321",
            "f_model_config": '{"api_key": "xxx", "api_url": "http://x", "api_model": "m"}',
        }])
        small_model_dao.name_check = mock.Mock(return_value=[])
        small_model_dao.edit_model_info = mock.Mock(return_value=None)
        # 控制器会 await redis_util.delete_str(...)；用 AsyncMock 避免真实连接
        redis_mock = mock.MagicMock()
        redis_mock.delete_str = mock.AsyncMock(return_value=None)
        small_model_controller.redis_util = redis_mock
        para = logics.EditExternalSmallModel(
            model_id="1234567890987654321",
            model_name="1",
            model_type="embedding",
            model_config={"api_url": "http://x", "api_model": "m"},
            batch_size=16,
            max_tokens=512,
            embedding_dim=768,
        )
        res = loop.run_until_complete(
            small_model_controller.edit_model(para, "1", "zh", "user"))
        self.assertEqual(isinstance(json.loads(res.body)["id"], str), True)


class TestGetInfoList(TestCase):
    def setUp(self) -> None:
        self.get_model_info_list = small_model_dao.get_model_info_list
        self.get_model_info_total = small_model_dao.get_model_info_total

    def tearDown(self) -> None:
        small_model_dao.get_model_info_list = self.get_model_info_list
        small_model_dao.get_model_info_total = self.get_model_info_total
        StandLogger.stand_log_shutdown()

    def test_get_info_list_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        small_model_dao.get_model_info_list = mock.Mock(return_value=[])
        small_model_dao.get_model_info_total = mock.Mock(return_value=[])
        res = loop.run_until_complete(
            small_model_controller.get_info_list("asc", "update_time", 1, 10, "1", "embedding", "baidu", "1", "user"))
        # 响应体为 {"count": int, "data": list}
        self.assertEqual(isinstance(json.loads(res.body)["data"], list), True)


class TestGetInfo(TestCase):
    def setUp(self) -> None:
        self.get_model_info_by_id = small_model_dao.get_model_info_by_id

    def tearDown(self) -> None:
        small_model_dao.get_model_info_by_id = self.get_model_info_by_id
        StandLogger.stand_log_shutdown()

    def test_get_info_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        small_model_dao.get_model_info_by_id = mock.Mock(return_value=[
            {
                "f_model_id": "1",
                "f_model_name": "1",
                "f_model_type": "embedding",
                "f_model_config": "{}",
                "f_create_time": datetime.today(),
                "f_update_time": datetime.today(),
                "f_adapter": 0,
                "f_adapter_code": None,
                "f_batch_size": 16,
                "f_max_tokens": 512,
                "f_embedding_dim": 768,
                "f_default": 0,
            }
        ])
        res = loop.run_until_complete(
            small_model_controller.get_info("1", "1", "user"))
        self.assertEqual(json.loads(res.body)["model_id"], "1")


class TestDeleteModel(TestCase):
    def setUp(self) -> None:
        self.get_model_info_by_ids = small_model_dao.get_model_info_by_ids
        self.delete_model_info_by_ids = small_model_dao.delete_model_info_by_ids
        self.get_all_ids = small_model_dao.get_all_ids
        self.redis_util = small_model_controller.redis_util

    def tearDown(self) -> None:
        small_model_dao.get_model_info_by_ids = self.get_model_info_by_ids
        small_model_dao.delete_model_info_by_ids = self.delete_model_info_by_ids
        small_model_dao.get_all_ids = self.get_all_ids
        small_model_controller.redis_util = self.redis_util
        StandLogger.stand_log_shutdown()

    def test_delete_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        small_model_dao.get_model_info_by_ids = mock.Mock(return_value=[{
            "f_model_id": "1", "f_model_name": "m1"}])
        small_model_dao.delete_model_info_by_ids = mock.Mock(return_value=None)
        # AUTH 关闭时 get_permission_ids 走 get_all_ids -> DB；需 mock 出可删 id 列表
        small_model_dao.get_all_ids = mock.Mock(return_value=[{"f_model_id": "1"}])
        redis_mock = mock.MagicMock()
        redis_mock.delete_str = mock.AsyncMock(return_value=None)
        small_model_controller.redis_util = redis_mock
        # 删除接口签名改为 (model_para: dict, userId, language, role)；删的是 id 列表
        res = loop.run_until_complete(
            small_model_controller.delete_model({"model_ids": ["1"]}, "1", "zh", "user"))
        self.assertEqual(json.loads(res.body)["id"], ["1"])


if __name__ == '__main__':
    import unittest

    unittest.main()
