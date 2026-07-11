"""测试 controller/model_audit_controller.py 模块"""
import json

import pytest
from unittest.mock import AsyncMock, Mock, patch
from app.controller.model_audit_controller import add_llm_model_call_log
from app.interfaces import logics

PRODUCE_PATH = 'app.controller.model_audit_controller.produce_metering_record'


class TestModelAuditController:
    """测试模型审计控制器（计量生产端走 produce_metering_record 抽象）"""

    @pytest.fixture
    def mock_audit_request(self):
        """创建审计请求的mock"""
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
        """测试成功添加LLM模型调用日志"""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            # 不应该抛出异常
            await add_llm_model_call_log(mock_audit_request)

            # 验证计量生产端被调用
            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_queue_full(self, mock_audit_request):
        """测试计量队列发送失败时的处理（丢弃不抛异常）"""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = False

            # 不应该抛出异常，只是记录警告
            await add_llm_model_call_log(mock_audit_request)

            mock_produce.assert_called_once()

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_exception(self, mock_audit_request):
        """测试添加日志时发生异常"""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.side_effect = Exception("metering transport error")

            # 不应该抛出异常，只是记录错误
            await add_llm_model_call_log(mock_audit_request)

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_message_format(self, mock_audit_request):
        """测试消息格式是否正确"""
        with patch(PRODUCE_PATH, new_callable=AsyncMock) as mock_produce:
            mock_produce.return_value = True

            await add_llm_model_call_log(mock_audit_request)

            # 获取调用参数
            call_args = mock_produce.call_args

            # 验证key和value都被正确编码为bytes
            assert 'value' in call_args.kwargs
            assert 'key' in call_args.kwargs
            assert isinstance(call_args.kwargs['value'], bytes)
            assert isinstance(call_args.kwargs['key'], bytes)

            # 验证消息体字段完整
            payload = json.loads(call_args.kwargs['value'].decode('utf-8'))
            assert payload['model_id'] == mock_audit_request.model_id
            assert payload['user_id'] == mock_audit_request.user_id
            assert payload['input_tokens'] == mock_audit_request.input_tokens
            assert payload['output_tokens'] == mock_audit_request.output_tokens
            assert payload['status'] == mock_audit_request.status

    @pytest.mark.asyncio
    async def test_add_llm_model_call_log_with_different_status(self):
        """测试不同状态的日志记录"""
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
        """测试零token的日志记录"""
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
        """测试大量token的日志记录"""
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
        """测试日志记录的时间消耗"""
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

            # 异步发送应该很快完成（小于1秒）
            assert elapsed < 1.0
