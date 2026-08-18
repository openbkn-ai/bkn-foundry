"""Tests for test_utils_common."""
import pytest
import os
from unittest.mock import Mock, patch
from app.utils.common import (
    GetCallerInfo,
    IsInPod,
    GetFailureThreshold,
    SetFailureThreshold,
    GetRecoveryTimeout,
    SetRecoveryTimeout,
    get_user_info,
    validate_required_params
)


class TestCommonFunctions:
    """Tests for test common functions."""

    def test_get_caller_info(self):
        """Test test get caller info."""
        filename, lineno = GetCallerInfo()
        # Verify the return value type.
        assert isinstance(filename, str)
        assert isinstance(lineno, int)
        assert lineno > 0

    def test_is_in_pod_true(self):
        """Test test is in pod true."""
        with patch.dict(os.environ, {
            'KUBERNETES_SERVICE_HOST': 'localhost',
            'KUBERNETES_SERVICE_PORT': '8080'
        }):
            assert IsInPod() is True

    def test_is_in_pod_false(self):
        """Test test is in pod false."""
        with patch.dict(os.environ, {}, clear=True):
            assert IsInPod() is False

    def test_is_in_pod_partial_env(self):
        """Test test is in pod partial env."""
        with patch.dict(os.environ, {'KUBERNETES_SERVICE_HOST': 'localhost'}, clear=True):
            assert IsInPod() is False

        with patch.dict(os.environ, {'KUBERNETES_SERVICE_PORT': '8080'}, clear=True):
            assert IsInPod() is False

    def test_failure_threshold_get_default(self):
        """Test test failure threshold get default."""
        threshold = GetFailureThreshold()
        assert isinstance(threshold, int)
        assert threshold >= 0

    def test_failure_threshold_set_and_get(self):
        """Test test failure threshold set and get."""
        original = GetFailureThreshold()
        try:
            SetFailureThreshold(20)
            assert GetFailureThreshold() == 20

            SetFailureThreshold(5)
            assert GetFailureThreshold() == 5
        finally:
            # Restore the original value.
            SetFailureThreshold(original)

    def test_recovery_timeout_get_default(self):
        """Test test recovery timeout get default."""
        timeout = GetRecoveryTimeout()
        assert isinstance(timeout, int)
        assert timeout >= 0

    def test_recovery_timeout_set_and_get(self):
        """Test test recovery timeout set and get."""
        original = GetRecoveryTimeout()
        try:
            SetRecoveryTimeout(10)
            assert GetRecoveryTimeout() == 10

            SetRecoveryTimeout(30)
            assert GetRecoveryTimeout() == 30
        finally:
            # Restore the original value.
            SetRecoveryTimeout(original)

    @pytest.mark.asyncio
    async def test_get_user_info_all_headers(self):
        """Test test get user info all headers."""
        request = Mock()
        request.headers = {
            'x-account-id': 'user123',
            'x-account-type': 'admin',
            'accept-language': 'en-US'
        }
        request.scope = {"state": {"effective_locale": "en-US"}}

        userId, language, role = await get_user_info(request)

        assert userId == 'user123'
        assert language == 'en-US'
        assert role == 'admin'

    @pytest.mark.asyncio
    async def test_get_user_info_missing_headers(self):
        """Test test get user info missing headers."""
        request = Mock()
        request.headers = {}

        userId, language, role = await get_user_info(request)

        assert userId == ""
        assert language == "zh-CN"  # Default value.
        assert role == ""

    @pytest.mark.asyncio
    async def test_get_user_info_partial_headers(self):
        """Test test get user info partial headers."""
        request = Mock()
        request.headers = {
            'x-account-id': 'user456'
        }

        userId, language, role = await get_user_info(request)

        assert userId == 'user456'
        assert language == "zh-CN"  # Default value.
        assert role == ""

    @pytest.mark.asyncio
    async def test_validate_required_params_all_present(self):
        """Test test validate required params all present."""
        params_dict = {
            'name': 'test',
            'age': 25,
            'email': 'test@example.com'
        }
        required_params = ['name', 'age']

        missing = await validate_required_params(params_dict, required_params)

        assert missing == []

    @pytest.mark.asyncio
    async def test_validate_required_params_some_missing(self):
        """Test test validate required params some missing."""
        params_dict = {
            'name': 'test'
        }
        required_params = ['name', 'age', 'email']

        missing = await validate_required_params(params_dict, required_params)

        assert set(missing) == {'age', 'email'}

    @pytest.mark.asyncio
    async def test_validate_required_params_all_missing(self):
        """Test test validate required params all missing."""
        params_dict = {}
        required_params = ['name', 'age', 'email']

        missing = await validate_required_params(params_dict, required_params)

        assert set(missing) == {'name', 'age', 'email'}

    @pytest.mark.asyncio
    async def test_validate_required_params_empty_required(self):
        """Test test validate required params empty required."""
        params_dict = {
            'name': 'test',
            'age': 25
        }
        required_params = []

        missing = await validate_required_params(params_dict, required_params)

        assert missing == []

    @pytest.mark.asyncio
    async def test_get_user_info_with_kwargs(self):
        """Test test get user info with kwargs."""
        request = Mock()
        request.headers = {
            'x-account-id': 'user789',
            'x-account-type': 'user',
            'accept-language': 'zh-CN'
        }

        # Test that passing extra kwargs does not cause errors.
        userId, language, role = await get_user_info(request, extra_param="test")

        assert userId == 'user789'
        assert language == 'zh-CN'
        assert role == 'user'

    @pytest.mark.asyncio
    async def test_get_user_info_uses_the_resolved_request_locale(self):
        request = Mock()
        request.headers = {'accept-language': 'zh-CN, en-US;q=0.8'}
        request.scope = {'state': {'effective_locale': 'en-US'}}

        _, language, _ = await get_user_info(request)

        assert language == 'en-US'

