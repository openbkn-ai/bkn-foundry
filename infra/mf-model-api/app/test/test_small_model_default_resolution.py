"""Tests for test_small_model_default_resolution."""
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
from app.utils.external_small_model_utils import UpstreamModelError


class TestDefaultSmallModelResolution:
    @pytest.mark.asyncio
    async def test_explicit_missing_model_returns_not_found(self):
        request = Mock(model="missing-model", model_id="", input=["text"])
        mock_redis = AsyncMock()
        mock_redis.get_str.return_value = None

        with patch("app.controller.small_model_controller.redis_util", mock_redis), \
                patch("app.controller.small_model_controller.small_model_dao.get_model_info_by_name_id",
                      return_value=[]):
            response = await embedding_model_used(request, "user1", "zh", "test", "embedding")

        assert response.status_code == 404
        assert json.loads(response.body)["code"] == \
            "ModelFactory.ExternalSmallModel.Used.NameNotExist"

    def test_unspecified_model_resolves_the_type_default(self):
        """Test test unspecified model resolves the type default."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = [{"f_model_name": "gte-rerank-v2"}]

            result = _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_default_by_type.assert_called_once_with(RERANKER_MODEL_TYPE)
            dao.get_model_info_by_name_id.assert_not_called()
            assert result[0]["f_model_name"] == "gte-rerank-v2"

    def test_explicit_model_still_queries_by_name(self):
        """Test test explicit model still queries by name."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "bge-reranker"}]

            result = _load_small_model(False, RERANKER_MODEL_TYPE, "bge-reranker", "")

            dao.get_model_info_by_name_id.assert_called_once_with("bge-reranker", "")
            dao.get_default_by_type.assert_not_called()
            assert result[0]["f_model_name"] == "bge-reranker"

    def test_unset_default_falls_back_to_the_legacy_name(self):
        """Test test unset default falls back to the legacy name."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "reranker"}]

            result = _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_model_info_by_name_id.assert_called_once_with("reranker", None)
            assert result[0]["f_model_name"] == "reranker"

    def test_configured_default_wins_over_the_legacy_name(self):
        """Test test configured default wins over the legacy name."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = [{"f_model_name": "gte-rerank-v2"}]

            _load_small_model(True, RERANKER_MODEL_TYPE, "", "")

            dao.get_model_info_by_name_id.assert_not_called()

    def test_no_legacy_name_for_an_unknown_type(self):
        """Test test no legacy name for an unknown type."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []

            result = _load_small_model(True, "ocr", "", "")

            dao.get_model_info_by_name_id.assert_not_called()
            assert result == []

    def test_legacy_fallback_covers_the_embedding_guess_too(self):
        """Test test legacy fallback covers the embedding guess too."""
        with patch("app.controller.small_model_controller.small_model_dao") as dao:
            dao.get_default_by_type.return_value = []
            dao.get_model_info_by_name_id.return_value = [{"f_model_name": "embedding"}]

            _load_small_model(True, "embedding", "", "")

            dao.get_model_info_by_name_id.assert_called_once_with("embedding", None)

    def test_missing_error_distinguishes_the_two_cases(self):
        """Test test missing error distinguishes the two cases."""
        assert _model_missing_error(False) is ModelFactory_ExternalSmallModel_Used_NameNotExist
        assert _model_missing_error(True) is ModelFactory_DefaultSmallModel_NotExist

    def test_default_pointer_is_cached_briefly(self):
        """Test test default pointer is cached briefly."""
        assert _model_cache_ttl(True) == DEFAULT_MODEL_CACHE_TTL_SECONDS
        assert _model_cache_ttl(False) == MODEL_CACHE_TTL_SECONDS
        assert DEFAULT_MODEL_CACHE_TTL_SECONDS < MODEL_CACHE_TTL_SECONDS


class TestEmbeddingProviderErrorMapping:
    def test_provider_summary_redacts_embedded_request_body(self):
        error = UpstreamModelError(400, json.dumps({"error": {"message": (
            "batch size is invalid; request body: {\"input\": [\"customer business data\"]}")}}))

        assert "batch size is invalid" in error.detail
        assert "customer business data" not in error.detail

    def test_provider_summary_keeps_input_parameter_limit(self):
        error = UpstreamModelError(400, json.dumps({"error": {"message": (
            "Invalid 'input': array too long. Expected an array with maximum length 2048.")}}))

        assert "maximum length 2048" in error.detail

    @pytest.mark.asyncio
    async def test_batch_limit_is_a_safe_parameter_error(self):
        request = Mock(model="embedding", model_id="", input=["customer business data"])
        redis = AsyncMock()
        redis.get_str.return_value = None
        model_info = [{
            "f_model_config": json.dumps({"api_url": "https://provider.example", "api_model": "embed"}),
            "f_adapter": False,
            "f_model_id": "embedding-id",
            "f_adapter_code": None,
            "f_embedding_dim": 1024,
        }]
        upstream = UpstreamModelError(400, json.dumps({"error": {"message": (
            "batch size is invalid, it should not be larger than 10; "
            "Authorization: Bearer sk-test-secret input=[customer business data]")}}))

        with patch("app.controller.small_model_controller.redis_util", redis), \
                patch("app.controller.small_model_controller.small_model_dao.get_model_info_by_name_id",
                      return_value=model_info), \
                patch("app.controller.small_model_controller.InnerClient") as client, \
                patch("app.controller.small_model_controller.get_logger", return_value=None):
            client.return_value.embedding = AsyncMock(side_effect=upstream)
            response = await embedding_model_used(request, "user1", "zh", "test", "embedding")

        body = json.loads(response.body)
        assert response.status_code == 400
        assert body["code"] == "ModelFactory.ExternalSmallModel.Used.InvalidParameter"
        assert "batch size is invalid" in body["detail"]
        assert "sk-test-secret" not in body["detail"]
        assert "customer business data" not in body["detail"]
        assert body["solution"] == "batch_size_limit: 10"

    @pytest.mark.asyncio
    async def test_input_array_limit_produces_batch_size_guidance(self):
        request = Mock(model="embedding", model_id="", input=["text"])
        redis = AsyncMock()
        redis.get_str.return_value = None
        model_info = [{
            "f_model_config": json.dumps({"api_url": "https://provider.example", "api_model": "embed"}),
            "f_adapter": False,
            "f_model_id": "embedding-id",
            "f_adapter_code": None,
            "f_embedding_dim": 1024,
        }]
        upstream = UpstreamModelError(400, json.dumps({"error": {"message": (
            "Invalid 'input': array too long. Expected an array with maximum length 2048.")}}))

        with patch("app.controller.small_model_controller.redis_util", redis), \
                patch("app.controller.small_model_controller.small_model_dao.get_model_info_by_name_id",
                      return_value=model_info), \
                patch("app.controller.small_model_controller.InnerClient") as client, \
                patch("app.controller.small_model_controller.get_logger", return_value=None):
            client.return_value.embedding = AsyncMock(side_effect=upstream)
            response = await embedding_model_used(request, "user1", "zh", "test", "embedding")

        body = json.loads(response.body)
        assert body["solution"] == "batch_size_limit: 2048"


class TestSmallModelDaoDefaultQuery:
    def test_query_filters_by_default_flag_and_type(self):
        """Test test query filters by default flag and type."""
        from app.dao.small_model_dao import SmallModelDao

        cursor = Mock()
        cursor.fetchall.return_value = [{"f_model_name": "gte-rerank-v2"}]

        # The decorator creates the connection; call the wrapped function body directly through __wrapped__ exposed by functools.wraps.
        SmallModelDao.get_default_by_type.__wrapped__(
            SmallModelDao(), RERANKER_MODEL_TYPE, Mock(), cursor
        )

        sql, params = cursor.execute.call_args[0]
        assert "f_default = 1" in sql
        assert "f_model_type = %s" in sql
        assert params == RERANKER_MODEL_TYPE

    def test_query_orders_so_the_result_is_deterministic(self):
        """Test test query orders so the result is deterministic."""
        from app.dao.small_model_dao import SmallModelDao

        cursor = Mock()
        cursor.fetchall.return_value = []

        SmallModelDao.get_default_by_type.__wrapped__(
            SmallModelDao(), RERANKER_MODEL_TYPE, Mock(), cursor
        )

        sql = cursor.execute.call_args[0][0]
        assert "order by f_update_time desc" in sql
