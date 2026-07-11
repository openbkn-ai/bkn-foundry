"""测试计量后端选择与 metering_producer 双后端行为"""
import pytest
from unittest.mock import AsyncMock, MagicMock, patch

from app.core.config import BaseConfig, base_config, resolve_metering_backend
from app.utils import metering_producer


class TestResolveMeteringBackend:
    """resolve_metering_backend 环境组合矩阵"""

    def test_explicit_kafka(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', 'kafka')
        monkeypatch.delenv('KAFKAHOST', raising=False)
        assert resolve_metering_backend() == 'kafka'

    def test_explicit_redis(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', 'redis')
        monkeypatch.setenv('KAFKAHOST', 'kafka-headless.resource')
        assert resolve_metering_backend() == 'redis'

    def test_auto_with_kafkahost(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', 'auto')
        monkeypatch.setenv('KAFKAHOST', 'kafka-headless.resource')
        assert resolve_metering_backend() == 'kafka'

    def test_auto_without_kafkahost(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', 'auto')
        monkeypatch.delenv('KAFKAHOST', raising=False)
        assert resolve_metering_backend() == 'redis'

    def test_invalid_value_falls_back_to_auto(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', 'rabbitmq')
        monkeypatch.delenv('KAFKAHOST', raising=False)
        assert resolve_metering_backend() == 'redis'

    def test_case_insensitive(self, monkeypatch):
        monkeypatch.setattr(BaseConfig, 'METERINGBACKEND', ' Kafka ')
        monkeypatch.delenv('KAFKAHOST', raising=False)
        assert resolve_metering_backend() == 'kafka'


class TestMeteringProducerRedis:
    """redis 后端生产路径"""

    @pytest.mark.asyncio
    async def test_xadd_called_with_stream_and_maxlen(self, monkeypatch):
        monkeypatch.setattr(metering_producer, '_backend', 'redis')
        mock_conn = MagicMock()
        mock_conn.xadd = AsyncMock()
        with patch.object(metering_producer, '_get_redis_conn',
                          new=AsyncMock(return_value=mock_conn)):
            ok = await metering_producer.produce_metering_record(b'{"a":1}', b'k1')

        assert ok is True
        mock_conn.xadd.assert_awaited_once()
        args, kwargs = mock_conn.xadd.call_args
        assert args[0] == metering_producer.METERING_STREAM
        assert args[1] == {'value': b'{"a":1}', 'key': b'k1'}
        assert kwargs['maxlen'] == base_config.METERINGSTREAMMAXLEN
        assert kwargs['approximate'] is True

    @pytest.mark.asyncio
    async def test_xadd_failure_returns_false(self, monkeypatch):
        """redis 异常时丢弃消息返回 False，不向上抛"""
        monkeypatch.setattr(metering_producer, '_backend', 'redis')
        mock_conn = MagicMock()
        mock_conn.xadd = AsyncMock(side_effect=Exception("redis down"))
        with patch.object(metering_producer, '_get_redis_conn',
                          new=AsyncMock(return_value=mock_conn)):
            ok = await metering_producer.produce_metering_record(b'{"a":1}')

        assert ok is False
        # 连接被置空，下次重建
        assert metering_producer._redis_conn is None

    @pytest.mark.asyncio
    async def test_no_key_field_when_key_absent(self, monkeypatch):
        monkeypatch.setattr(metering_producer, '_backend', 'redis')
        mock_conn = MagicMock()
        mock_conn.xadd = AsyncMock()
        with patch.object(metering_producer, '_get_redis_conn',
                          new=AsyncMock(return_value=mock_conn)):
            await metering_producer.produce_metering_record(b'{"a":1}')

        args, _ = mock_conn.xadd.call_args
        assert args[1] == {'value': b'{"a":1}'}


class TestMeteringProducerKafka:
    """kafka 后端生产路径（委托现有 kafka_client）"""

    @pytest.mark.asyncio
    async def test_delegates_to_kafka_client(self, monkeypatch):
        monkeypatch.setattr(metering_producer, '_backend', 'kafka')
        mock_client = MagicMock()
        mock_client.produce_async.return_value = True
        with patch('app.mydb.ConnectUtil.kafka_client', mock_client):
            ok = await metering_producer.produce_metering_record(b'v', b'k')

        assert ok is True
        mock_client.produce_async.assert_called_once_with(value=b'v', key=b'k')

    @pytest.mark.asyncio
    async def test_kafka_client_none_returns_false(self, monkeypatch):
        """后端为 kafka 但客户端未初始化时降级丢弃"""
        monkeypatch.setattr(metering_producer, '_backend', 'kafka')
        with patch('app.mydb.ConnectUtil.kafka_client', None):
            ok = await metering_producer.produce_metering_record(b'v')

        assert ok is False
