"""Tests for test_utils_param_verify."""
import pytest
from app.utils.param_verify_utils import (
    verify_icon_color_config,
    verify_icon_color_config_metric,
    verify_text_field,
    verify_id,
    include_dataset_id,
    llm_source_verify
)


class TestVerifyIconColorConfig:
    """Tests for test verify icon color config."""

    def test_valid_colors(self):
        """Test test valid colors."""
        valid_colors = [
            "icon-color-pz-019688",
            "icon-color-pz-F759AB",
            "icon-color-pz-FADB14",
            "icon-color-pz-FF8501",
            "icon-color-pz-F75959",
            "icon-color-pz-8C8C8C",
            "icon-color-pz-126EE3",
            "icon-color-pz-13C2C2",
            "icon-color-pz-52C41A",
            "icon-color-pz-9254DE"
        ]
        for color in valid_colors:
            assert verify_icon_color_config(color) is True

    def test_invalid_colors(self):
        """Test test invalid colors."""
        invalid_colors = [
            "icon-color-pz-FFFFFF",
            "invalid-color",
            "",
            "icon-color-zbk-FF8501",  # Wrong prefix.
            "icon-color-pz-"
        ]
        for color in invalid_colors:
            assert verify_icon_color_config(color) is False


class TestVerifyIconColorConfigMetric:
    """Tests for test verify icon color config metric."""

    def test_valid_metric_colors(self):
        """Test test valid metric colors."""
        valid_colors = [
            "icon-color-zbk-FF8501",
            "icon-color-zbk-13C2C2",
            "icon-color-zbk-FADB14",
            "icon-color-zbk-019688",
            "icon-color-zbk-9254DE",
            "icon-color-zbk-8C8C8C",
            "icon-color-zbk-126EE3",
            "icon-color-zbk-52C41A",
            "icon-color-zbk-F759AB",
            "icon-color-zbk-F75959"
        ]
        for color in valid_colors:
            assert verify_icon_color_config_metric(color) is True

    def test_invalid_metric_colors(self):
        """Test test invalid metric colors."""
        invalid_colors = [
            "icon-color-zbk-FFFFFF",
            "invalid-color",
            "",
            "icon-color-pz-FF8501",  # Wrong prefix.
            "icon-color-zbk-"
        ]
        for color in invalid_colors:
            assert verify_icon_color_config_metric(color) is False


class TestVerifyTextField:
    """Tests for test verify text field."""

    def test_valid_text_within_limit(self):
        """Test test valid text within limit."""
        assert verify_text_field("测试文本", 10) is True
        assert verify_text_field("test", 100) is True
        assert verify_text_field("", 10) is True

    def test_valid_text_exact_limit(self):
        """Test test valid text exact limit."""
        assert verify_text_field("a" * 50, 50) is True

    def test_text_exceeds_limit(self):
        """Test test text exceeds limit."""
        assert verify_text_field("a" * 101, 100) is False

    def test_valid_characters(self):
        """Test test valid characters."""
        valid_strings = [
            "hello123",
            "你好世界",
            "test@test.com",
            "!@#$%^&*()",
            "中英文混合123"
        ]
        for s in valid_strings:
            assert verify_text_field(s, 100) is True

    def test_invalid_type(self):
        """Test test invalid type."""
        assert verify_text_field(123, 10) is False
        assert verify_text_field(None, 10) is False
        assert verify_text_field([], 10) is False

    def test_special_characters(self):
        """Test test special characters."""
        assert verify_text_field("测试-文本_123", 50) is True
        assert verify_text_field("test/path", 50) is True


class TestVerifyId:
    """Tests for test verify id."""

    def test_valid_18_digit_id(self):
        """Test test valid 18 digit id."""
        assert verify_id("123456789012345678") is True

    def test_valid_19_digit_id(self):
        """Test test valid 19 digit id."""
        assert verify_id("1234567890123456789") is True

    def test_invalid_length(self):
        """Test test invalid length."""
        assert verify_id("12345") is False
        assert verify_id("12345678901234567") is False  # 17 digits.
        assert verify_id("12345678901234567890") is False  # 20 digits.

    def test_invalid_characters(self):
        """Test test invalid characters."""
        assert verify_id("12345678901234567a") is False
        assert verify_id("123456789012345-78") is False
        assert verify_id("123456789012345 78") is False

    def test_invalid_type(self):
        """Test test invalid type."""
        assert verify_id(123456789012345678) is False
        assert verify_id(None) is False
        assert verify_id([]) is False


class TestIncludeDatasetId:
    """Tests for test include dataset id."""

    def test_dataset_id_found(self):
        """Test test dataset id found."""
        dataset_version_id_list = ["123/v1", "456/v2", "789/v3"]
        assert include_dataset_id(dataset_version_id_list, "123") is True
        assert include_dataset_id(dataset_version_id_list, "456") is True

    def test_dataset_id_not_found(self):
        """Test test dataset id not found."""
        dataset_version_id_list = ["123/v1", "456/v2"]
        assert include_dataset_id(dataset_version_id_list, "999") is False

    def test_empty_list(self):
        """Test test empty list."""
        assert include_dataset_id([], "123") is False

    def test_invalid_format(self):
        """Test test invalid format."""
        dataset_version_id_list = ["invalid", "no-slash"]
        result = include_dataset_id(dataset_version_id_list, "123")
        assert result is False

    def test_exception_handling(self):
        """Test test exception handling."""
        # Pass a non-list.
        assert include_dataset_id("not a list", "123") is False


class TestLlmSourceVerify:
    """Tests for test llm source verify."""

    def test_valid_parameters(self):
        """Test test valid parameters."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="10",
            rule="update_time",
            series="openai",
            name="test_model",
            model_type="llm"
        )
        assert result is False  # Validation success returns False.

    def test_invalid_page(self):
        """Test test invalid page."""
        result = llm_source_verify(
            order="desc",
            page="0",  # Invalid.
            size="10",
            rule="update_time",
            series="openai",
            name="",
            model_type=""
        )
        assert result is not False  # Should return an error.

    def test_invalid_size(self):
        """Test test invalid size."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="abc",  # Invalid.
            rule="update_time",
            series="openai",
            name="",
            model_type=""
        )
        assert result is not False

    def test_invalid_order(self):
        """Test test invalid order."""
        result = llm_source_verify(
            order="invalid",  # Invalid.
            page="1",
            size="10",
            rule="update_time",
            series="openai",
            name="",
            model_type=""
        )
        assert result is not False

    def test_invalid_rule(self):
        """Test test invalid rule."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="10",
            rule="invalid",  # Invalid.
            series="openai",
            name="",
            model_type=""
        )
        assert result is not False

    def test_empty_series(self):
        """Test test empty series."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="10",
            rule="update_time",
            series="",  # Invalid.
            name="",
            model_type=""
        )
        assert result is not False

    def test_invalid_model_type(self):
        """Test test invalid model type."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="10",
            rule="update_time",
            series="openai",
            name="",
            model_type="invalid"  # Invalid.
        )
        assert result is not False

    def test_valid_model_types(self):
        """Test test valid model types."""
        for model_type in ["llm", "rlm", "vu", ""]:
            result = llm_source_verify(
                order="desc",
                page="1",
                size="10",
                rule="update_time",
                series="openai",
                name="",
                model_type=model_type
            )
            # An empty string is valid.
            if model_type == "":
                assert result is False

    def test_name_with_special_characters(self):
        """Test test name with special characters."""
        result = llm_source_verify(
            order="desc",
            page="1",
            size="10",
            rule="update_time",
            series="openai",
            name="测试模型123!@#",
            model_type=""
        )
        # Should pass validation.
        assert result is False

