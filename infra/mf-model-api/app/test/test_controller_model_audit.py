"""Tests for test_controller_model_audit."""
import json

import pytest
from unittest.mock import AsyncMock, Mock, patch
from app.controller.model_audit_controller import add_llm_model_call_log
from app.interfaces import logics

PRODUCE_PATH = 'app.controller.model_audit_controller.produce_metering_record'


class TestModelAuditController:
    """Tests for test model audit controller."""

    @pytest.fixture
    def mock_audit_request(self):
        """Test mock audit request."""
        request = Mock(spec=logics.AddModelUsedAudit)
        request.model_id = "123456789012345678"
        request.user_id = "user123"
        request.input_tokens = 100
        request.output_tokens = 50
        request.total_time = 1.5
        request.first_time = 0.5
        request.status = "success"
        return request

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_success(self, mock_audit_request):
        """Test test add llm model call log success."""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            # Should not raise an exception.
            await add_llm_model_call_log(mock_audit_request)

            # Verify the metering producer is called.
            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_queue_full(self, mock_audit_request):
        """Test test add llm model call log queue full."""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = False

            # Should not raise an exception; only record a warning.
            await add_llm_model_call_log(mock_audit_request)

            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_exception(self, mock_audit_request):
        """Test test add llm model call log exception."""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.side_effect = Exception("metering transport error")

            # Should not raise an exception; only record an error.
            await add_llm_model_call_log(mock_audit_request)

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_message_format(self, mock_audit_request):
        """Test test add llm model call log message format."""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            await add_llm_model_call_log(mock_audit_request)

            # Get call arguments.
            call_args = mock_produce.call_args

            # Verify key and value are correctly encoded as bytes.
            assert 'value' in call_args.kwargs
            assert 'key' in call_args.kwargs
            assert isinstance(call_args.kwargs['value'], bytes)
            assert isinstance(call_args.kwargs['key'], bytes)

            # Verify message body fields are complete.
            payload = json.loads(call_args.kwargs['value'].decode('utf-8'))
            assert payload['model_id'] == mock_audit_request.model_id
            assert payload['user_id'] == mock_audit_request.user_id
            assert payload['input_tokens'] == mock_audit_request.input_tokens
            assert payload['output_tokens'] == mock_audit_request.output_tokens
            assert payload['status'] == mock_audit_request.status

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_with_different_status(self):
        """Test test add llm model call log with different status."""
        for status in ['success', 'failed', 'timeout']:
            request = Mock(spec=logics.AddModelUsedAudit)
            request.model_id = "123456789012345678"
            request.user_id = "user123"
            request.input_tokens = 100
            request.output_tokens = 50
            request.total_time = 1.5
            request.first_time = 0.5
            request.status = status

            with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
                mock_produce.return_value = True

                await add_llm_model_call_log(request)

                mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_zero_tokens(self):
        """Test test add llm model call log zero tokens."""
        request = Mock(spec=logics.AddModelUsedAudit)
        request.model_id = "123456789012345678"
        request.user_id = "user123"
        request.input_tokens = 0
        request.output_tokens = 0
        request.total_time = 0.1
        request.first_time = 0.1
        request.status = "success"

        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            await add_llm_model_call_log(request)

            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_large_tokens(self):
        """Test test add llm model call log large tokens."""
        request = Mock(spec=logics.AddModelUsedAudit)
        request.model_id = "123456789012345678"
        request.user_id = "user123"
        request.input_tokens = 100000
        request.output_tokens = 50000
        request.total_time = 30.0
        request.first_time = 5.0
        request.status = "success"

        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            await add_llm_model_call_log(request)

            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_timing(self):
        """Test test add llm model call log timing."""
        request = Mock(spec=logics.AddModelUsedAudit)
        request.model_id = "123456789012345678"
        request.user_id = "user123"
        request.input_tokens = 100
        request.output_tokens = 50
        request.total_time = 1.5
        request.first_time = 0.5
        request.status = "success"

        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            import time
            start = time.time()
            await add_llm_model_call_log(request)
            elapsed = time.time() - start

            # Async sending should complete quickly, under one second.
            assert elapsed < 1.0
