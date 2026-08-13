"""不指定模型时按类型解析系统默认小模型（#842）。

在此之前 reranker 端点收到空 model 直接 400，逼得每个调用方硬编码一个猜出来的名字
兜底（context-loader 猜 "reranker"、vega 猜 "embedding"），注册名一改就全线
NameNotExist——而管理员在模型管理里勾的默认反倒没人读。
"""
from unittest.mock import patch

from app.controller.small_model_controller import (
    DEFAULT_MODEL_CACHE_TTL_SECONDS,
    MODEL_CACHE_TTL_SECONDS,
    RERANKER_MODEL_TYPE,
    _load_small_model,
    _model_cache_ttl,
    _model_missing_error,
)
from app.commons.errors import (
    ModelFactory_DefaultSmallModel_NotExist,
    ModelFactory_ExternalSmallModel_Used_NameNotExist,
)


class TestDefaultSmallModelResolution:
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
