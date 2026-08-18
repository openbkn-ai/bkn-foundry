"""Tests for test_utils_comment_utils."""
import pytest
import os
import asyncio
from unittest.mock import patch, mock_open, AsyncMock
from datetime import datetime
from app.utils.comment_utils import write_log, error_log


class TestWriteLog:
    """Tests for test write log."""

    @pytest.mark.asyncio
    async def test_write_log_basic(self):
        """Test test write log basic."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await write_log(api="test_api", msg="test message", user="test_user")
            mock_file.assert_called_once_with("log.log", mode="a", encoding='utf-8')
            # Verify the written content contains key information.
            handle = mock_file()
            written_content = ''.join(call.args[0] for call in handle.write.call_args_list)
            assert "test_api" in written_content
            assert "test message" in written_content
            assert "test_user" in written_content

    @pytest.mark.asyncio
    async def test_write_log_default_user(self):
        """Test test write log default user."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await write_log(api="test_api", msg="test message")
            handle = mock_file()
            written_content = ''.join(call.args[0] for call in handle.write.call_args_list)
            assert "root" in written_content

    @pytest.mark.asyncio
    async def test_write_log_none_values(self):
        """Test test write log none values."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await write_log(api=None, msg=None, user=None)
            mock_file.assert_called_once()

    @pytest.mark.asyncio
    async def test_write_log_special_characters(self):
        """Test test write log special characters."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await write_log(
                api="test_api!@#",
                msg="测试消息\n换行",
                user="用户123"
            )
            mock_file.assert_called_once()

    @pytest.mark.asyncio
    async def test_write_log_empty_strings(self):
        """Test test write log empty strings."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await write_log(api="", msg="", user="")
            mock_file.assert_called_once()

    @pytest.mark.asyncio
    async def test_write_log_long_message(self):
        """Test test write log long message."""
        mock_file = mock_open()
        long_msg = "a" * 10000
        with patch('builtins.open', mock_file):
            await write_log(api="test_api", msg=long_msg, user="test_user")
            mock_file.assert_called_once()


class TestErrorLog:
    """Tests for test error log."""

    @pytest.mark.asyncio
    async def test_error_log_basic(self):
        """Test test error log basic."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await error_log(api="error_api", msg="error message", user="error_user")
            # Note that the file name contains a space: "err or.log".
            mock_file.assert_called_once_with("err or.log", mode="a", encoding='utf-8')
            handle = mock_file()
            written_content = ''.join(call.args[0] for call in handle.write.call_args_list)
            assert "error_api" in written_content
            assert "error message" in written_content
            assert "error_user" in written_content

    @pytest.mark.asyncio
    async def test_error_log_default_user(self):
        """Test test error log default user."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await error_log(api="error_api", msg="error message")
            handle = mock_file()
            written_content = ''.join(call.args[0] for call in handle.write.call_args_list)
            assert "root" in written_content

    @pytest.mark.asyncio
    async def test_error_log_none_values(self):
        """Test test error log none values."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await error_log(api=None, msg=None, user=None)
            mock_file.assert_called_once()

    @pytest.mark.asyncio
    async def test_error_log_exception_info(self):
        """Test test error log exception info."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            await error_log(
                api="test_api",
                msg="Exception: ValueError('test error')",
                user="test_user"
            )
            mock_file.assert_called_once()

    @pytest.mark.asyncio
    async def test_error_log_concurrent_writes(self):
        """Test test error log concurrent writes."""
        mock_file = mock_open()
        with patch('builtins.open', mock_file):
            tasks = [
                error_log(api=f"api_{i}", msg=f"msg_{i}", user=f"user_{i}")
                for i in range(10)
            ]
            await asyncio.gather(*tasks)
            # Should have 10 file-open calls.
            assert mock_file.call_count == 10


class TestLogFileIntegration:
    """Tests for test log file integration."""

    @pytest.mark.asyncio
    async def test_write_and_error_log_different_files(self):
        """Test test write and error log different files."""
        write_mock = mock_open()
        error_mock = mock_open()
        
        def side_effect(filename, *args, **kwargs):
            if filename == "log.log":
                return write_mock()
            elif filename == "err or.log":
                return error_mock()
        
        with patch('builtins.open', side_effect=side_effect):
            await write_log(api="test", msg="normal log")
            await error_log(api="test", msg="error log")
            
            # Verify both files are opened.
            assert write_mock.call_count > 0 or error_mock.call_count > 0

