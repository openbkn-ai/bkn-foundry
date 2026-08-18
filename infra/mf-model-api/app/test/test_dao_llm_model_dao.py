"""Tests for test_dao_llm_model_dao."""
import pytest
from unittest.mock import Mock, patch, MagicMock
import json
from datetime import datetime
from app.dao.llm_model_dao import ModelDao, llm_model_dao


class TestModelDao:
    """Tests for test model dao."""

    @pytest.fixture
    def dao(self):
        """Test dao."""
        return ModelDao()

    @pytest.fixture
    def mock_cursor(self):
        """Test mock cursor."""
        cursor = Mock()
        cursor.fetchall = Mock(return_value=[])
        cursor.fetchone = Mock(return_value=None)
        cursor.execute = Mock()
        return cursor

    def test_get_model_name_by_id(self, dao, mock_cursor):
        """Test test get model name by id."""
        mock_cursor.fetchall.return_value = [{"f_model_name": "test_model"}]
        with patch.object(dao, 'get_model_name_by_id', return_value="test_model"):
            result = dao.get_model_name_by_id("123")
            assert result == "test_model"

    def test_get_model_id_by_name(self, dao, mock_cursor):
        """Test test get model id by name."""
        expected = [{"f_model_id": "123456789012345678"}]
        mock_cursor.fetchall.return_value = expected
        with patch.object(dao, 'get_model_id_by_name', return_value=expected):
            result = dao.get_model_id_by_name("test_model")
            assert result == expected

    def test_get_model_series_by_id(self, dao):
        """Test test get model series by id."""
        with patch.object(dao, 'get_model_series_by_id', return_value="openai"):
            result = dao.get_model_series_by_id("123")
            assert result == "openai"

    def test_get_all_model_list(self, dao):
        """Test test get all model list."""
        expected = [{
            "f_model_id": "123",
            "f_model_name": "test_model",
            "f_model_series": "openai",
            "f_model_type": "llm"
        }]
        with patch.object(dao, 'get_all_model_list', return_value=expected):
            result = dao.get_all_model_list()
            assert len(result) == 1
            assert result[0]["f_model_name"] == "test_model"

    def test_get_data_from_model_list_by_id(self, dao):
        """Test test get data from model list by id."""
        expected = [{
            "f_model_id": "123",
            "f_model_name": "test_model",
            "f_model_config": '{"api_url": "http://test.com"}',
            "f_max_model_len": 4096
        }]
        with patch.object(dao, 'get_data_from_model_list_by_id', return_value=expected):
            result = dao.get_data_from_model_list_by_id("123")
            assert len(result) == 1

    def test_get_data_from_model_list_by_name_id_with_name(self, dao):
        """Test test get data from model list by name id with name."""
        expected = [{"f_model_id": "123", "f_model_name": "test_model"}]
        with patch.object(dao, 'get_data_from_model_list_by_name_id', return_value=expected):
            result = dao.get_data_from_model_list_by_name_id("test_model", None)
            assert len(result) == 1

    def test_get_data_from_model_list_by_name_id_with_id(self, dao):
        """Test test get data from model list by name id with id."""
        expected = [{"f_model_id": "123", "f_model_name": "test_model"}]
        with patch.object(dao, 'get_data_from_model_list_by_name_id', return_value=expected):
            result = dao.get_data_from_model_list_by_name_id(None, "123")
            assert len(result) == 1

    def test_delete_model_by_id(self, dao):
        """Test test delete model by id."""
        with patch.object(dao, 'delete_model_by_id', return_value=None):
            result = dao.delete_model_by_id(["123", "456"])
            assert result is None

    def test_get_model_by_name(self, dao):
        """Test test get model by name."""
        expected = [{"f_model_name": "test_model", "f_model_id": "123"}]
        with patch.object(dao, 'get_model_by_name', return_value=expected):
            result = dao.get_model_by_name("test_model")
            assert len(result) == 1

    def test_check_model_is_exist_true(self, dao):
        """Test test check model is exist true."""
        with patch.object(dao, 'check_model_is_exist', return_value=True):
            result = dao.check_model_is_exist("123")
            assert result is True

    def test_check_model_is_exist_false(self, dao):
        """Test test check model is exist false."""
        with patch.object(dao, 'check_model_is_exist', return_value=False):
            result = dao.check_model_is_exist("999")
            assert result is False

    def test_check_model_unique_duplicate(self, dao):
        """Test test check model unique duplicate."""
        with patch.object(dao, 'check_model_unique', return_value=True):
            result = dao.check_model_unique("http://test.com", "gpt-3.5", "user1", "key123")
            assert result is True

    def test_check_model_unique_not_duplicate(self, dao):
        """Test test check model unique not duplicate."""
        with patch.object(dao, 'check_model_unique', return_value=False):
            result = dao.check_model_unique("http://new.com", "gpt-4", "user1", "key456")
            assert result is False

    def test_get_model_default_paras(self, dao):
        """Test test get model default paras."""
        expected = {
            "123": {"model_name": "test_model", "model_series": "openai", "model": "gpt-3.5"}
        }
        with patch.object(dao, 'get_model_default_paras', return_value=expected):
            result = dao.get_model_default_paras()
            assert "123" in result

    def test_get_all_tome_model_list(self, dao):
        """Test test get all tome model list."""
        expected = [{"f_model_name": "tome_model", "f_model_series": "tome"}]
        with patch.object(dao, 'get_all_tome_model_list', return_value=expected):
            result = dao.get_all_tome_model_list()
            assert len(result) == 1

    def test_get_quota_by_user_and_model_exists(self, dao):
        """Test test get quota by user and model exists."""
        expected = [{
            "f_input_tokens": 1000000,
            "used_input_tokens": 5000,
            "f_output_tokens": 1000000,
            "used_output_tokens": 3000,
            "f_billing_type": 1,
            "f_num_type": "[0, 0]",
            "remaining_input_tokens": 995000,
            "remaining_output_tokens": 997000,
            "total_input_tokens": 1000000,
            "total_output_tokens": 1000000
        }]
        with patch.object(dao, 'get_quota_by_user_and_model', return_value=expected):
            result = dao.get_quota_by_user_and_model("user1", "model123")
            assert len(result) == 1
            assert result[0]["remaining_input_tokens"] == 995000

    def test_get_quota_by_user_and_model_not_exists(self, dao):
        """Test test get quota by user and model not exists."""
        with patch.object(dao, 'get_quota_by_user_and_model', return_value=[]):
            result = dao.get_quota_by_user_and_model("user1", "model999")
            assert len(result) == 0

    def test_get_data_from_default_model(self, dao):
        """Test test get data from default model."""
        expected = [{
            "f_model_id": "default123",
            "f_model_name": "default_model",
            "f_default": 1
        }]
        with patch.object(dao, 'get_data_from_default_model', return_value=expected):
            result = dao.get_data_from_default_model()
            assert len(result) == 1


class TestLlmModelDaoInstance:
    """Tests for test llm model dao instance."""

    def test_instance_exists(self):
        """Test test instance exists."""
        assert llm_model_dao is not None
        assert isinstance(llm_model_dao, ModelDao)

    def test_instance_has_methods(self):
        """Test test instance has methods."""
        assert hasattr(llm_model_dao, 'get_model_name_by_id')
        assert hasattr(llm_model_dao, 'get_all_model_list')
        assert hasattr(llm_model_dao, 'delete_model_by_id')
        assert hasattr(llm_model_dao, 'check_model_is_exist')

