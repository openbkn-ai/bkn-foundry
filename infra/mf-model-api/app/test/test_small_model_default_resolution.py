"""不指定模型时按类型解析系统默认小模型（#842）。

在此之前 reranker 端点收到空 model 直接 400，逼得每个调用方硬编码一个猜出来的名字
兜底（context-loader 猜 "reranker"、vega 猜 "embedding"），注册名一改就全线
NameNotExist——而管理员在模型管理里勾的默认反倒没人读。
"""
import json
from unittest.mock import AsyncMock, Mock, patch

import pytest

from app.controller.small_model_controller import (
    DEFAULT_MODEL_CACHE_TTL_SECONDS,
    MODEL_CACHE_TTL_SECONDS,
    RERANKER_MODEL_TYPE,
    _load_small_model,
    _model_cache_ttl,
    _model_missing_error,
    embedding_model_used,
)
from app.commons.errors import (
    ModelFactory_DefaultSmallModel_NotExist,
    ModelFactory_ExternalSmallModel_Used_NameNotExist,
)


class TestDefaultSmallModelResolution:
    @pytest.mark.asyncio
    async def test_explicit_missing_model_returns_not_found(self):
        request = Mock(model="missing-model", model_id="", input=["text"])
        mock_redis = AsyncMock()
        mock_redis.get_str.return_value = None

        with patch("app.controller.small_model_controller.redis_util", mock_redis), \
                patch("app.controller.small_model_controller.small_model_dao.get_model_info_by_name_id",
                      return_value=[]):
            response = await embedding_model_used(request, "user1", "zh", "test")

        assert response.status_code == 404
        assert json.loads(response.body)["code"] == \
            "ModelFactory.ExternalSmallModel.Used.NameNotExist"

    def test_unspecified_model_resolves_the_type_default(self):
        """既没给名字也没给 id 时，走 f_default=1 那条，而不是按名字瞎查。"""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = [{"f_model_name": "gte-rerank-v2"}]

            result = _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_default_by_type.assert_called_once_with(RERANKER_MODEL_TYPE)
            dao.get_model_info_by_name_id.assert_not_called()
            assert result[0]["f_model_name"] == "gte-rerank-v2"

    def test_explicit_model_still_queries_by_name(self):
        """显式指定仍按名字/ID 查——默认解析只是兜底，不能覆盖调用方的选择。"""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "bge-reranker"}]

            result = _load_small_model(False, RERANKER_MODEL_TYPE, "bge-reranker", "")

            dao.get_model_info_by_name_id.assert_called_once_with("bge-reranker", "")
            dao.get_default_by_type.assert_not_called()
            assert result[0]["f_model_name"] == "bge-reranker"

    def test_unset_default_falls_back_to_the_legacy_name(self):
        """存量库里 f_default 全是 0，也没有迁移回填它。

        默认解析落空就直接 400 的话，升级当天精排即静默退回原序——调用方都是优雅
        降级只打 warn。所以这里按老约定的名字再兜一次。
        """
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "reranker"}]

            result = _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_model_info_by_name_id.assert_called_once_with("reranker", None)
            assert result[0]["f_model_name"] == "reranker"

    def test_configured_default_wins_over_the_legacy_name(self):
        """管理员勾了默认就以它为准，旧名字那一跳根本不该发生。"""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = [{"f_model_name": "gte-rerank-v2"}]

            _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_model_info_by_name_id.assert_not_called()

    def test_no_legacy_name_for_an_unknown_type(self):
        """老约定只存在于 reranker / embedding 两类，别的类型不许瞎猜名字。"""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []

            result = _load_small_model(True, "ocr", "", "")

            dao.get_model_info_by_name_id.assert_not_called()
            assert result == []

    def test_legacy_fallback_covers_the_embedding_guess_too(self):
        """vega 猜的是 "embedding"（#296），同样得兜住。"""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "embedding"}]

            _load_small_model(True, "embedding", "", "")

            dao.get_model_info_by_name_id.assert_called_once_with("embedding", None)

    def test_missing_error_distinguishes_the_two_cases(self):
        """「你指定的模型不存在」与「管理员没配默认」处理方式不同，错误码不能混。"""
        assert _model_missing_error(False) is ModelFactory_ExternalSmallModel_Used_NameNotExist
        assert _model_missing_error(True) is ModelFactory_DefaultSmallModel_NotExist

    def test_default_pointer_is_cached_briefly(self):
        """默认模型是管理员随时可改的指针，不是某个模型的配置。

        指针缓存一天就会出现「换了默认、一天之内仍按旧的调」——#552 记的正是这个坑。
        """
        assert _model_cache_ttl(True) == DEFAULT_MODEL_CACHE_TTL_SECONDS
        assert _model_cache_ttl(False) == MODEL_CACHE_TTL_SECONDS
        assert DEFAULT_MODEL_CACHE_TTL_SECONDS < MODEL_CACHE_TTL_SECONDS


class TestSmallModelDaoDefaultQuery:
    def test_query_filters_by_default_flag_and_type(self):
        """SQL 必须同时按 f_default=1 与 f_model_type 过滤。

        只按 f_default 查会串类型：embedding 与 reranker 各有各的默认，
        查错类型的后果是拿一个 embedding 去做重排。
        """
        from app.dao.small_model_dao import SmallModelDao

        cursor = Mock()
        cursor.fetchall.return_value = [{"f_model_name": "gte-rerank-v2"}]

        # 装饰器负责建连接，这里直接调用被包裹的函数体（靠 functools.wraps 暴露的 __wrapped__）
        SmallModelDao.get_default_by_type.__wrapped__(
            SmallModelDao(), RERANKER_MODEL_TYPE, Mock(), cursor
        )

        sql, params = cursor.execute.call_args[0]
        assert "f_default = 1" in sql
        assert "f_model_type = %s" in sql
        assert params == RERANKER_MODEL_TYPE

    def test_query_orders_so_the_result_is_deterministic(self):
        """管理端清旧默认与置新默认是两次独立提交，中途失败会留下多行 f_default=1。

        没有 order by 时取 [0] 拿到哪一行由存储引擎决定，此后默认解析结果不确定。
        """
        from app.dao.small_model_dao import SmallModelDao

        cursor = Mock()
        cursor.fetchall.return_value = []

        SmallModelDao.get_default_by_type.__wrapped__(
            SmallModelDao(), RERANKER_MODEL_TYPE, Mock(), cursor
        )

        sql = cursor.execute.call_args[0][0]
        assert "order by f_update_time desc" in sql
