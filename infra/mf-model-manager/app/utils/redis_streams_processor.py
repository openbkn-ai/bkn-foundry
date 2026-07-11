"""
Redis Stream 计量消费端：与 KafkaStreamsProcessor 对等的实现（METERING_BACKEND=redis 时启用）。

- 消费组语义：XREADGROUP + 批量 XACK，at-least-once；落库侧 INSERT ON DUPLICATE KEY 幂等兜底。
- 进程重启不丢：启动时先以 id=0 读回自己名下未 ACK 的 pending。
- 实例崩溃不丢：周期 XAUTOCLAIM 接管空闲超时的 pending。
- 聚合与落库复用 QuotaAggregator，与 Kafka 消费端口径一致。
"""
import asyncio
import json
import os
import socket
import time

from app.core.config import base_config
from app.logs.stand_log import StandLogger
from app.utils.quota_aggregator import QuotaAggregator

# stream 名沿用原 Kafka topic 名（与生产端 metering_producer.METERING_STREAM 一致）
STREAM_NAME = 'tenant_a.dip.model_manager.quota_data'
GROUP_ID = 'quota_data_group_new'
AUTOCLAIM_INTERVAL_SECONDS = 300
AUTOCLAIM_MIN_IDLE_MS = 600000  # 空闲超 10 分钟的 pending 才接管


class RedisStreamsProcessor:
    def __init__(self, stream_name=STREAM_NAME, group_id=GROUP_ID):
        self.stream_name = stream_name
        self.group_id = group_id
        self.consumer_name = f"{socket.gethostname()}-{os.getpid()}"
        self.aggregator = QuotaAggregator()
        self.running = True
        self.conn = None

    async def _connect(self):
        from app.mydb.ConnectUtil import RedisClient
        self.conn = await RedisClient().connect_redis_async(
            base_config.METERINGREDISDB, 'write')

    async def _ensure_group(self):
        try:
            await self.conn.xgroup_create(self.stream_name, self.group_id, id='0', mkstream=True)
            StandLogger.info_log(f"创建消费组 {self.group_id}@{self.stream_name}")
        except Exception as e:
            if 'BUSYGROUP' in str(e):
                StandLogger.info_log(f"消费组 {self.group_id} 已存在")
            else:
                raise

    def _handle_entry(self, entry_id, fields):
        """处理一条 stream entry（解析失败只记日志，不阻塞后续 ACK）"""
        raw = fields.get(b'value') if isinstance(fields, dict) else None
        if raw is None and isinstance(fields, dict):
            raw = fields.get('value')
        if raw is None:
            StandLogger.warn(f"计量消息缺少 value 字段，跳过: {entry_id}")
            return
        if isinstance(raw, bytes):
            raw = raw.decode('utf-8')
        try:
            data = json.loads(raw)
            self.aggregator.add_record(data)
        except json.JSONDecodeError as e:
            StandLogger.error(f"解析计量消息失败: {e}")
        except Exception as e:
            StandLogger.error(f"处理计量消息时出错: {e}")

    def _consume_entries(self, entries):
        """处理 XREADGROUP 返回的批次，返回待 ACK 的 entry id 列表"""
        ack_ids = []
        for _stream_key, messages in entries:
            for entry_id, fields in messages:
                self._handle_entry(entry_id, fields)
                ack_ids.append(entry_id)
        return ack_ids

    async def _drain_own_pending(self):
        """进程重启后，先处理自己名下未 ACK 的消息"""
        last_id = '0'
        while self.running:
            entries = await self.conn.xreadgroup(
                self.group_id, self.consumer_name, {self.stream_name: last_id}, count=500)
            if not entries or not entries[0][1]:
                break
            ack_ids = self._consume_entries(entries)
            if not ack_ids:
                break
            await self.conn.xack(self.stream_name, self.group_id, *ack_ids)
            last_id = ack_ids[-1]
            StandLogger.info_log(f"重放 {len(ack_ids)} 条重启前未确认的计量消息")

    async def _autoclaim_stale_pending(self):
        """接管崩溃实例遗留的 pending（失败不影响主消费循环）"""
        try:
            result = await self.conn.xautoclaim(
                self.stream_name, self.group_id, self.consumer_name,
                min_idle_time=AUTOCLAIM_MIN_IDLE_MS, start_id='0-0', count=500)
            # redis 6.2 返回 (next_start, claimed)；redis 7 追加 deleted 列表
            claimed = result[1] if result and len(result) >= 2 else []
            ack_ids = []
            for entry_id, fields in claimed:
                if fields is None:
                    continue  # 已被 XDEL/XTRIM 截断的消息
                self._handle_entry(entry_id, fields)
                ack_ids.append(entry_id)
            if ack_ids:
                await self.conn.xack(self.stream_name, self.group_id, *ack_ids)
                StandLogger.info_log(f"接管并处理 {len(ack_ids)} 条空闲 pending 计量消息")
        except Exception as e:
            StandLogger.warn(f"XAUTOCLAIM 处理失败（不影响主消费）: {e}")

    async def start(self):
        """启动消费主循环（阻塞直至 stop）"""
        StandLogger.info_log(
            f"启动 Redis Stream 计量消费者... stream={self.stream_name}, "
            f"group={self.group_id}, consumer={self.consumer_name}")
        await self._connect()
        await self._ensure_group()
        self.aggregator.start_periodic_flush()
        await self._drain_own_pending()

        last_autoclaim = time.monotonic()
        while self.running:
            try:
                entries = await self.conn.xreadgroup(
                    self.group_id, self.consumer_name, {self.stream_name: '>'},
                    count=500, block=200)
                if entries:
                    ack_ids = self._consume_entries(entries)
                    if ack_ids:
                        await self.conn.xack(self.stream_name, self.group_id, *ack_ids)

                if time.monotonic() - last_autoclaim >= AUTOCLAIM_INTERVAL_SECONDS:
                    last_autoclaim = time.monotonic()
                    await self._autoclaim_stale_pending()
            except Exception as e:
                StandLogger.error(f"消费 Redis Stream 消息时出错: {e}")
                await asyncio.sleep(1)
                try:
                    await self._connect()
                    await self._ensure_group()
                except Exception as ce:
                    StandLogger.error(f"重连 Redis 失败: {ce}")

        StandLogger.info_log("Redis Stream 计量消费者已停止")

    def stop_consumer(self):
        """停止消费者（信号处理器调用，主循环在 block 超时后退出）"""
        StandLogger.info_log("停止 Redis Stream 计量消费者...")
        self.running = False
        self.aggregator.stop()


# 全局实例（与 kafka_streams_processor.kafka_processor 对等）
redis_processor = None


def start_redis_streams_processor():
    """启动 Redis Stream 计量处理器（阻塞运行）"""
    global redis_processor
    if redis_processor is None:
        StandLogger.info_log("创建 RedisStreamsProcessor 实例...")
        redis_processor = RedisStreamsProcessor()
        asyncio.run(redis_processor.start())
    else:
        StandLogger.info_log("RedisStreamsProcessor 实例已存在，跳过创建")
