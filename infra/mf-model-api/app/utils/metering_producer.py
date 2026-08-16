"""Produce metering records through Kafka or Redis Stream.

Transport failures retain the Kafka path's behavior: log the failure, return
False, and do not block the model invocation.
"""
import asyncio

from app.core.config import base_config, resolve_metering_backend
from app.logs.stand_log import StandLogger

# Reuse the Kafka topic name for operational consistency across transports.
METERING_STREAM = 'tenant_a.dip.model_manager.quota_data'

_backend = resolve_metering_backend()
_redis_conn = None
_redis_conn_lock = asyncio.Lock()


def metering_backend():
    return _backend


async def _get_redis_conn():
    global _redis_conn
    if _redis_conn is not None:
        return _redis_conn
    async with _redis_conn_lock:
        if _redis_conn is None:
            from app.mydb.ConnectUtil import RedisClient
            _redis_conn = await RedisClient().connect_redis_async(
                base_config.METERINGREDISDB, 'write')
    return _redis_conn


async def produce_metering_record(value: bytes, key: bytes = None) -> bool:
    """Send one metering record and report whether it was queued."""
    global _redis_conn
    if _backend == 'kafka':
        from app.mydb.ConnectUtil import kafka_client
        if kafka_client is None:
            StandLogger.warn("计量后端为 kafka 但客户端未初始化，丢弃计量消息")
            return False
        return kafka_client.produce_async(value=value, key=key)

    try:
        conn = await _get_redis_conn()
        fields = {'value': value}
        if key is not None:
            fields['key'] = key
        await conn.xadd(
            METERING_STREAM,
            fields,
            maxlen=base_config.METERINGSTREAMMAXLEN,
            approximate=True,
        )
        return True
    except Exception as e:
        # Match Kafka degradation: discard and warn without failing the model call.
        _redis_conn = None
        StandLogger.warn(f"写入计量 Redis Stream 失败，丢弃消息: {e}")
        return False
