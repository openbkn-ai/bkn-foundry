"""Tests for test_utils_str_util."""
import pytest
from app.utils.str_util import generate_random_string, get_md5_key, has_common_substring


class TestGenerateRandomString:
    """Tests for test generate random string."""

    def test_default_length(self):
        """Test test default length."""
        result = generate_random_string()
        assert len(result) == 32
        assert result.isalnum()

    def test_custom_length(self):
        """Test test custom length."""
        for length in [10, 20, 50, 100]:
            result = generate_random_string(length)
            assert len(result) == length
            assert result.isalnum()

    def test_zero_length(self):
        """Test test zero length."""
        result = generate_random_string(0)
        assert len(result) == 0
        assert result == ""

    def test_randomness(self):
        """Test test randomness."""
        results = set()
        for _ in range(100):
            results.add(generate_random_string(32))
        # Should generate 100 different strings.
        assert len(results) == 100

    def test_valid_characters(self):
        """Test test valid characters."""
        result = generate_random_string(1000)
        valid_chars = set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
        assert all(c in valid_chars for c in result)


class TestGetMd5Key:
    """Tests for test get md5 key."""

    def test_simple_string(self):
        """Test test simple string."""
        result = get_md5_key("hello")
        assert isinstance(result, str)
        assert len(result) == 32
        # The MD5 hash should be stable.
        assert result == get_md5_key("hello")

    def test_empty_string(self):
        """Test test empty string."""
        result = get_md5_key("")
        assert isinstance(result, str)
        assert len(result) == 32

    def test_chinese_string(self):
        """Test test chinese string."""
        result = get_md5_key("你好世界")
        assert isinstance(result, str)
        assert len(result) == 32

    def test_special_characters(self):
        """Test test special characters."""
        result = get_md5_key("!@#$%^&*()")
        assert isinstance(result, str)
        assert len(result) == 32

    def test_long_string(self):
        """Test test long string."""
        long_str = "a" * 10000
        result = get_md5_key(long_str)
        assert isinstance(result, str)
        assert len(result) == 32

    def test_consistency(self):
        """Test test consistency."""
        input_str = "test_consistency"
        result1 = get_md5_key(input_str)
        result2 = get_md5_key(input_str)
        assert result1 == result2

    def test_different_inputs(self):
        """Test test different inputs."""
        result1 = get_md5_key("test1")
        result2 = get_md5_key("test2")
        assert result1 != result2


class TestHasCommonSubstring:
    """Tests for test has common substring."""

    def test_has_common_suffix_prefix(self):
        """Test test has common suffix prefix."""
        assert has_common_substring("hello", "hello world") is True
        assert has_common_substring("world", "hello") is False

    def test_exact_match(self):
        """Test test exact match."""
        assert has_common_substring("test", "test") is True

    def test_no_common_substring(self):
        """Test test no common substring."""
        assert has_common_substring("abc", "def") is False

    def test_partial_overlap(self):
        """Test test partial overlap."""
        assert has_common_substring("abc", "bcd") is True
        assert has_common_substring("hello", "orld") is True

    def test_empty_strings(self):
        """Test test empty strings."""
        assert has_common_substring("", "") is False
        assert has_common_substring("test", "") is False
        assert has_common_substring("", "test") is False

    def test_single_character(self):
        """Test test single character."""
        assert has_common_substring("a", "a") is True
        assert has_common_substring("a", "b") is False

    def test_chinese_characters(self):
        """Test test chinese characters."""
        assert has_common_substring("你好", "好世界") is True
        assert has_common_substring("你好", "世界") is False

    def test_case_sensitive(self):
        """Test test case sensitive."""
        assert has_common_substring("Hello", "hello") is False
        assert has_common_substring("TEST", "EST") is True

    def test_longer_first_string(self):
        """Test test longer first string."""
        # The suffix "world" of "hello world" matches the prefix of "world".
        assert has_common_substring("hello world", "world") is True
        # The suffix "ing" of "testing" matches the prefix of "ing".
        assert has_common_substring("testing", "ing") is True

    def test_multiple_character_overlap(self):
        """Test test multiple character overlap."""
        assert has_common_substring("abcde", "cdefg") is True
        assert has_common_substring("12345", "45678") is True

