from datetime import datetime
from unittest import TestCase, mock

from sse_starlette import EventSourceResponse

from app.dao.llm_model_dao import llm_model_dao
from app.logs.stand_log import StandLogger
from app.utils import llm_utils, verify_utils
from app.controller import llm_controller
import asyncio
import json


def _msg(content, role):
    # used_model_openai 直接访问 tool_calls/tool_call_id 键，缺失会 KeyError，需显式补 None
    return {"content": content, "role": role, "tool_calls": None, "tool_call_id": None}


class TestUsedModelOpenai(TestCase):
    def setUp(self) -> None:
        self.get_data_from_model_list_by_name = llm_model_dao.get_data_from_model_list_by_name
        self.OtherClient = llm_utils.OtherClient
        self.redis_util = llm_controller.redis_util

    def tearDown(self) -> None:
        llm_model_dao.get_data_from_model_list_by_name = self.get_data_from_model_list_by_name
        llm_utils.OtherClient = self.OtherClient
        llm_controller.redis_util = self.redis_util
        StandLogger.stand_log_shutdown()

    def _redis_mock(self):
        # get_str 返回 None -> 走 DB 分支；delete_str/set_str 为 async
        redis_mock = mock.MagicMock()
        redis_mock.get_str = mock.AsyncMock(return_value=None)
        redis_mock.set_str = mock.AsyncMock(return_value=None)
        redis_mock.delete_str = mock.AsyncMock(return_value=None)
        return redis_mock

    def test_used_model_openai_success1(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_controller.redis_util = self._redis_mock()
        request = {
            "messages": [
                _msg("You are a helpful assistant", "system"),
                _msg("Hi", "user"),
            ],
            "model": "test_model",
            "frequency_penalty": 0,
            "max_tokens": 2048,
            "presence_penalty": 0,
            "stream": False,
            "temperature": 1,
            "top_p": 1,
            "top_k": 1,
            "response_format": {},
            "cache": False,
            "stop": None
        }
        llm_model_dao.get_data_from_model_list_by_name = mock.Mock(return_value=[{
            "f_model_id": "1234567890987654321",
            "f_model_name": "test_model",
            "f_model_series": "others",
            "f_model_type": "chat",
            "f_model_config": '{"api_key": "xxx", "api_model": "qianxun-l-128k", "api_type": "openai", "api_url": "https://qianxun.rcrai.com/open/qianxun/v1/chat/completions"}',
            "f_model": "qianxun-l-128k",
            "f_max_model_len": 32,
            "f_model_parameters": 72,
            "f_quota": 0
        }])
        m1 = mock.MagicMock()
        m1.chat_completion = mock.AsyncMock(return_value={})
        llm_utils.OtherClient = mock.Mock(return_value=m1)
        res = loop.run_until_complete(
            llm_controller.used_model_openai(request, "111", "zh", "test"))
        self.assertEqual({}, json.loads(res.body))

    def test_used_model_openai_success2(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_controller.redis_util = self._redis_mock()
        request = {
            "messages": [
                _msg("You are a helpful assistant", "system"),
                _msg("Hi", "user"),
            ],
            "model": "test_model",
            "frequency_penalty": 0,
            "max_tokens": 2048,
            "presence_penalty": 0,
            "stream": True,
            "temperature": 1,
            "top_p": 1,
            "top_k": 1,
            "cache": False,
            "stop": None,
            "response_format": None
        }
        llm_model_dao.get_data_from_model_list_by_name = mock.Mock(return_value=[{
            "f_model_id": "1234567890987654321",
            "f_model_name": "test_model",
            "f_model_series": "openai",
            "f_model_type": "chat",
            "f_model_config": '{"api_key": "xxx", "api_model": "qianxun-l-128k", "api_type": "openai", "api_url": "https://qianxun.rcrai.com/open/qianxun/v1/chat/completions"}',
            "f_model": "gpt-35-turbo-16k",
            "f_max_model_len": 32,
            "f_model_parameters": 72,
            "f_quota": 0
        }])
        res = loop.run_until_complete(
            llm_controller.used_model_openai(request, "111", "zh", "test"))
        self.assertEqual(isinstance(res, EventSourceResponse), True)


class TestAddModel(TestCase):
    def setUp(self) -> None:
        self.add_data_into_model_list = llm_model_dao.add_data_into_model_list
        self.get_model_by_name = llm_model_dao.get_model_by_name
        self.check_model_unique = llm_model_dao.check_model_unique

    def tearDown(self) -> None:
        llm_model_dao.add_data_into_model_list = self.add_data_into_model_list
        llm_model_dao.get_model_by_name = self.get_model_by_name
        llm_model_dao.check_model_unique = self.check_model_unique
        StandLogger.stand_log_shutdown()

    def test_add_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        request = {
            "model_config": {
                "api_model": "qianxun-l-128k",
                "api_url": "https://qianxun.rcrai.com/open/qianxun/v1",
                "api_key": "ckm-652a0795c43b8abca48ce7627d65e910",
                "api_type": "openai"
            },
            "model_series": "openai",
            "model_name": "qianxun-l-128k",
            "model_type": "llm",
            "max_model_len": 128,
            "quota": True
        }
        llm_model_dao.add_data_into_model_list = mock.Mock(return_value=None)
        llm_model_dao.get_model_by_name = mock.Mock(return_value=())
        llm_model_dao.check_model_unique = mock.Mock(return_value=False)
        # add_model 返回 {"status": "ok", "id": str(model_id)}
        res = loop.run_until_complete(
            llm_controller.add_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["status"], "ok")
        self.assertEqual(isinstance(json.loads(res.body)["id"], str), True)


# test_model
class TestTestModel(TestCase):
    def setUp(self) -> None:
        self.get_data_from_model_list_by_id = llm_model_dao.get_data_from_model_list_by_id
        self.ClientSession = verify_utils.aiohttp.ClientSession

    def tearDown(self) -> None:
        llm_model_dao.get_data_from_model_list_by_id = self.get_data_from_model_list_by_id
        verify_utils.aiohttp.ClientSession = self.ClientSession
        StandLogger.stand_log_shutdown()

    class _Response:
        def __init__(self, status, body="{}"):
            self.status = status
            self.body = body
            self.encoding = None
            self.content = []

        async def __aenter__(self):
            return self

        async def __aexit__(self, exc_type, exc, tb):
            return False

        async def text(self):
            return self.body

    class _Session:
        def __init__(self, response):
            self.response = response
            self.post_args = None
            self.post_kwargs = None

        async def __aenter__(self):
            return self

        async def __aexit__(self, exc_type, exc, tb):
            return False

        def post(self, *args, **kwargs):
            self.post_args = args
            self.post_kwargs = kwargs
            return self.response

    def test_test_model_success_openai(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        session = self._Session(self._Response(200))
        verify_utils.aiohttp.ClientSession = mock.Mock(return_value=session)
        request = {"model_id": "111"}
        llm_model_dao.get_data_from_model_list_by_id = mock.Mock(return_value=[{
            "f_model_id": "111",
            "f_create_by": "111",
            "f_is_delete": 0,
            "f_model_config": '{"api_key": "111", "api_model": "gpt-4-32k", "api_url": "https://artificial-intelligence-01.openai.azure.com/"}',
            "f_model_series": "openai",
            "f_model_type": "chat",
        }])
        res = loop.run_until_complete(
            llm_controller.test_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["status"], "ok")
        self.assertEqual(session.post_args[0],
                         "https://artificial-intelligence-01.openai.azure.com/"
                         "openai/deployments/gpt-4-32k/chat/completions"
                         "?api-version=2023-05-15&api-type=azure")
        self.assertEqual(session.post_kwargs["json"]["model"], "gpt-4-32k")
        self.assertEqual(session.post_kwargs["headers"], {"api-key": "111"})

    def test_test_model_fail_openai_non_200(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        session = self._Session(self._Response(401, '{"error":"invalid api key"}'))
        verify_utils.aiohttp.ClientSession = mock.Mock(return_value=session)
        request = {"model_id": "111"}
        llm_model_dao.get_data_from_model_list_by_id = mock.Mock(return_value=[{
            "f_model_id": "111",
            "f_create_by": "111",
            "f_is_delete": 0,
            "f_model_config": '{"api_key": "bad-key", "api_model": "bad-model", "api_url": "https://example.invalid/v1/chat/completions"}',
            "f_model_series": "openai",
            "f_model_type": "chat",
        }])
        res = loop.run_until_complete(
            llm_controller.test_model(request, "111", "zh"))
        self.assertEqual(res.status_code, 400)
        self.assertEqual(json.loads(res.body)["code"], "ModelFactory.ModelController.TestModel.Error")

    def test_test_model_fail_unreachable(self):
        # 非 openai series 走真实 HTTP，连接失败 -> 返回 TestModel.Error
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        request = {"model_id": "111"}
        llm_model_dao.get_data_from_model_list_by_id = mock.Mock(return_value=[{
            "f_model_id": "111",
            "f_create_by": "111",
            "f_is_delete": 0,
            "f_model_config": '{"api_key": "111", "api_model": "AIshuReader", "api_url": "http://127.0.0.1:1/v1"}',
            "f_model_series": "others",
            "f_model_type": "llm",
        }])
        res = loop.run_until_complete(
            llm_controller.test_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["code"], "ModelFactory.ModelController.TestModel.Error")


class TestEditModel(TestCase):
    def setUp(self) -> None:
        self.get_all_model_list = llm_model_dao.get_all_model_list
        self.get_data_from_model_list_by_id = llm_model_dao.get_data_from_model_list_by_id
        self.get_model_by_name = llm_model_dao.get_model_by_name
        self.edit_model = llm_model_dao.edit_model
        self.redis_util = llm_controller.redis_util

    def tearDown(self) -> None:
        llm_model_dao.get_all_model_list = self.get_all_model_list
        llm_model_dao.get_data_from_model_list_by_id = self.get_data_from_model_list_by_id
        llm_model_dao.get_model_by_name = self.get_model_by_name
        llm_model_dao.edit_model = self.edit_model
        llm_controller.redis_util = self.redis_util
        StandLogger.stand_log_shutdown()

    def test_edit_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        redis_mock = mock.MagicMock()
        redis_mock.delete_str = mock.AsyncMock(return_value=None)
        llm_controller.redis_util = redis_mock
        request = {
            "model_name": "gpt-35s",
            "model_config": {
                "api_model": "gpt-4-32k",
                "api_url": "https://artificial-intelligence-01.openai.azure.com/",
                "api_type": "openai",
                "api_key": "111"
            },
            "model_series": "aishu",
            "model_type": "llm",
            "icon": "azure",
            "model_id": "111",
            "max_model_len": 32,
            "quota": False
        }
        llm_model_dao.get_all_model_list = mock.Mock(return_value=[{"f_model_id": "111", "f_model_name": "111"}])
        llm_model_dao.get_data_from_model_list_by_id = mock.Mock(return_value=[{
            "f_model_id": "111", "f_create_by": "111", "f_is_delete": 0,
            "f_model_config": '{"api_key": "111", "api_model": "gpt-4-32k", "api_base": "https://artificial-intelligence-01.openai.azure.com/"}',
            "f_model_series": "aishu", "f_model_name": "111", "f_quota": False}])
        llm_model_dao.edit_model = mock.Mock(return_value=None)
        res = loop.run_until_complete(
            llm_controller.edit_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["status"], "ok")

    def test_edit_model_fail1(self):
        # model_type 非法 -> llm_edit_verify 返回 LLMEdit.ParameterError
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        request = {
            "model_name": "gpt-35s",
            "model_config": {
                "api_model": "gpt-4-32k",
                "api_url": "https://artificial-intelligence-01.openai.azure.com/",
                "api_type": "aishu",
                "api_key": "111"
            },
            "model_series": "openai",
            "model_type": "invalid_type",
            "icon": "azure",
            "model_id": "111",
            "max_model_len": 32,
            "quota": False
        }
        res = loop.run_until_complete(
            llm_controller.edit_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["code"], "ModelFactory.ConnectController.LLMEdit.ParameterError")

    def test_edit_model_fail2(self):
        # max_model_len 非法 -> llm_edit_verify 返回 LLMEdit.ParameterError
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        request = {
            "model_name": "gpt-35s",
            "model_config": {
                "api_model": "gpt-4-32k1",
                "api_url": "https://artificial-intelligence-01.openai.azure.com/",
                "api_key": "111"
            },
            "model_series": "aishu-m",
            "model_type": "llm",
            "icon": "azure",
            "model_id": "111",
            "max_model_len": 0,
            "quota": False
        }
        res = loop.run_until_complete(
            llm_controller.edit_model(request, "111", "zh"))
        self.assertEqual(json.loads(res.body)["code"], "ModelFactory.ConnectController.LLMEdit.ParameterError")


class TestSourceModel(TestCase):
    def setUp(self) -> None:
        self.get_data_from_model_list_by_name_fuzzy = llm_model_dao.get_data_from_model_list_by_name_fuzzy

    def tearDown(self) -> None:
        llm_model_dao.get_data_from_model_list_by_name_fuzzy = self.get_data_from_model_list_by_name_fuzzy
        StandLogger.stand_log_shutdown()

    def _row(self):
        return {
            "f_model_id": "111",
            "f_model_name": "111",
            "f_model_series": "aishu",
            "f_model": "AIshuReader",
            "f_model_api": "111",
            "f_create_by": "111",
            "f_update_by": "111",
            "f_create_time": datetime.today(),
            "f_update_time": datetime.today(),
            "f_icon": "aishu",
            "f_quota": 0,
            "f_max_model_len": 32,
            "f_model_parameters": 72,
            "f_model_type": "llm",
            "f_default": 0,
            "f_model_config": '{"api_key": "111", "api_model": "AIshuReader", "api_url": "http://x/v1"}'
        }

    def test_source_model_success1(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_model_dao.get_data_from_model_list_by_name_fuzzy = mock.Mock(return_value=[self._row()])
        res = loop.run_until_complete(
            llm_controller.source_model("111", "zh", "1", "10", "", "desc", "all", "update_time", "AIshuReader", None, 0))
        self.assertEqual(json.loads(res.body)["data"][0]["model_id"], "111")

    def test_source_model_success2(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_model_dao.get_data_from_model_list_by_name_fuzzy = mock.Mock(return_value=[self._row()])
        res = loop.run_until_complete(
            llm_controller.source_model("111", "zh", "1", "10", "", "desc", "aishu", "update_time", "AIshuReader", None, 0))
        self.assertEqual(json.loads(res.body)["data"][0]["model_id"], "111")


class TestCheckModel(TestCase):
    def setUp(self) -> None:
        self.get_all_model_list = llm_model_dao.get_all_model_list
        self.get_data_from_model_list_by_id = llm_model_dao.get_data_from_model_list_by_id

    def tearDown(self) -> None:
        llm_model_dao.get_all_model_list = self.get_all_model_list
        llm_model_dao.get_data_from_model_list_by_id = self.get_data_from_model_list_by_id
        StandLogger.stand_log_shutdown()

    def test_check_model_success(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_model_dao.get_data_from_model_list_by_id = mock.Mock(return_value=[{
            "f_model_id": "111", "f_create_by": "111", "f_is_delete": 0,
            "f_model_config": '{"api_key": "111", "api_model": "gpt-4-32k", "api_base": "https://artificial-intelligence-01.openai.azure.com/"}',
            "f_model_series": "aishu", "f_model_name": "111", "f_model_url": "1", "f_icon": "aishu",
            "f_quota": 0, "f_max_model_len": 32, "f_model_parameters": 72, "f_model_type": "llm"}])
        llm_model_dao.get_all_model_list = mock.Mock(return_value=[{
            "f_model_id": "111", "f_model_name": "111", "f_model": "AIshuReader", "f_create_by": "111",
            "f_max_model_len": 32, "f_model_parameters": 72}])
        res = loop.run_until_complete(
            llm_controller.check_model("111", "111", "zh"))
        self.assertEqual(json.loads(res.body)["model_id"], "111")


class TestEditDefaultModel(TestCase):
    def setUp(self) -> None:
        self.check_model_is_exist = llm_model_dao.check_model_is_exist
        self.get_default_model = llm_model_dao.get_default_model
        self.update_model_default_status = llm_model_dao.update_model_default_status
        self.redis_util = llm_controller.redis_util

    def tearDown(self) -> None:
        llm_model_dao.check_model_is_exist = self.check_model_is_exist
        llm_model_dao.get_default_model = self.get_default_model
        llm_model_dao.update_model_default_status = self.update_model_default_status
        llm_controller.redis_util = self.redis_util
        StandLogger.stand_log_shutdown()

    def _redis_mock(self):
        redis_mock = mock.MagicMock()
        redis_mock.delete_str = mock.AsyncMock(return_value=None)
        return redis_mock

    def test_edit_default_model_unsets_current_default(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_controller.redis_util = self._redis_mock()
        llm_model_dao.check_model_is_exist = mock.Mock(return_value=True)
        llm_model_dao.get_default_model = mock.Mock(return_value=[{"f_model_id": "111"}])
        llm_model_dao.update_model_default_status = mock.Mock(return_value=None)

        res = loop.run_until_complete(
            llm_controller.edit_default_model({"model_id": "111", "default": False}, "111", "zh"))

        body = json.loads(res.body)
        self.assertEqual(res.status_code, 200)
        self.assertEqual(body["default"], False)
        llm_model_dao.update_model_default_status.assert_called_once_with("111", False)
        llm_model_dao.get_default_model.assert_not_called()

    def test_edit_default_model_rejects_duplicate_set_default(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_controller.redis_util = self._redis_mock()
        llm_model_dao.check_model_is_exist = mock.Mock(return_value=True)
        llm_model_dao.get_default_model = mock.Mock(return_value=[{"f_model_id": "111"}])
        llm_model_dao.update_model_default_status = mock.Mock(return_value=None)

        res = loop.run_until_complete(
            llm_controller.edit_default_model({"model_id": "111", "default": True}, "111", "zh"))

        self.assertEqual(res.status_code, 400)
        llm_model_dao.update_model_default_status.assert_not_called()


class TestModelOverviewData(TestCase):
    def setUp(self) -> None:
        self.get_overview_data = llm_model_dao.get_overview_data

    def tearDown(self) -> None:
        llm_model_dao.get_overview_data = self.get_overview_data
        StandLogger.stand_log_shutdown()

    def test_get_overview_data_rejects_reversed_date_range(self):
        loop = asyncio.new_event_loop()
        asyncio.set_event_loop(loop)
        llm_model_dao.get_overview_data = mock.Mock(return_value=([], [], []))

        res = loop.run_until_complete(
            llm_controller.get_overview_data("111", "zh", "", "2026-07-14", "2026-07-13"))

        body = json.loads(res.body)
        self.assertEqual(res.status_code, 400)
        self.assertEqual(body["detail"], "Param start_time must be earlier than or equal to end_time")
        llm_model_dao.get_overview_data.assert_not_called()


if __name__ == '__main__':
    import unittest

    unittest.main()
