#!/usr/bin/env python3
"""
独立的计量消费进程启动脚本。

按 METERING_BACKEND 启动对应的消费端：
- kafka: KafkaStreamsProcessor（现状行为）
- redis: RedisStreamsProcessor（Redis Stream 消费组）
文件名保留 kafka_consumer_process.py 以兼容 main.py 的子进程拉起路径。
"""
import os
import sys
import signal
import multiprocessing
from app.logs.stand_log import StandLogger
from app.core.config import base_config, resolve_metering_backend
from app.utils.config_cache import quota_config_cache_tree  # 初始化配置缓存


class MeteringConsumerProcess:
    def __init__(self):
        self.process = None
        self.backend = resolve_metering_backend()
        self.running = False

    def signal_handler(self, signum, frame):
        """信号处理器，用于优雅关闭"""
        StandLogger.info_log(f"收到信号 {signum}，开始优雅关闭计量消费者...")
        self.running = False
        try:
            if self.backend == 'kafka':
                from app.utils.kafka_streams_processor import kafka_processor
                if kafka_processor:
                    kafka_processor.stop_consumer()
            else:
                from app.utils.redis_streams_processor import redis_processor
                if redis_processor:
                    redis_processor.stop_consumer()
        except Exception as e:
            StandLogger.error(f"停止计量处理器时出错: {e}")

    def run_consumer(self):
        """运行计量消费者的函数"""
        try:
            StandLogger.info_log(f"计量消费进程启动，后端: {self.backend}")
            self.running = True

            # 注册信号处理器
            signal.signal(signal.SIGINT, self.signal_handler)
            signal.signal(signal.SIGTERM, self.signal_handler)
            StandLogger.info_log("信号处理器已注册")
            if self.backend == 'kafka':
                StandLogger.info_log("正在导入 Kafka Streams 处理器...")
                from app.utils.kafka_streams_processor import start_kafka_streams_processor
                StandLogger.info_log("开始启动 Kafka Streams 处理器...")
                start_kafka_streams_processor()
                StandLogger.info_log("Kafka Streams 处理器启动完成")
            else:
                StandLogger.info_log("正在导入 Redis Streams 处理器...")
                from app.utils.redis_streams_processor import start_redis_streams_processor
                StandLogger.info_log("开始启动 Redis Streams 处理器...")
                start_redis_streams_processor()
                StandLogger.info_log("Redis Streams 处理器启动完成")

        except Exception as e:
            StandLogger.error(f"计量消费进程运行出错: {e}")
            import traceback
            StandLogger.error(f"详细错误信息: {traceback.format_exc()}")
            raise
        finally:
            StandLogger.info_log("计量消费进程结束")

    def start(self):
        """启动计量消费进程"""
        try:
            # 直接运行消费者
            self.run_consumer()
        except KeyboardInterrupt:
            StandLogger.info_log("收到键盘中断信号，关闭计量消费者")
        except Exception as e:
            StandLogger.error(f"启动计量消费进程失败: {e}")
            sys.exit(1)


# 兼容旧名（main.py 及外部脚本可能引用）
KafkaConsumerProcess = MeteringConsumerProcess


def main():
    """主函数"""
    StandLogger.info_log("=== 计量消费进程启动 ===")  # 控制台输出
    StandLogger.info_log("启动独立的计量消费进程")

    # 创建并启动计量消费者
    StandLogger.info_log("创建 MeteringConsumerProcess 实例...")  # 控制台输出
    consumer_process = MeteringConsumerProcess()
    StandLogger.info_log("开始启动消费者...")  # 控制台输出
    consumer_process.start()


if __name__ == '__main__':
    main()
