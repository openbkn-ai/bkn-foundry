"""
计量记录生产端：按 METERING_BACKEND 选择 Kafka 或 Redis Stream 传输。

失败语义与原 Kafka 路径一致：记日志返回 False，不阻塞模型调用。
"""
import asyncio

from app.core.config import base_config, resolve_metering_backend
from app.logs.stand_log import StandLogger

# stream 名沿用原 Kafka topic 名，便于运维排查与后续统一治理
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
    """发送一条计量记录，返回是否成功入队/入流。"""
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
        # 与 Kafka 路径同级降级：丢弃并告警，不影响模型调用；连接可能已坏，下次重建
        _redis_conn = None
        StandLogger.warn(f"写入计量 Redis Stream 失败，丢弃消息: {e}")
        return False
