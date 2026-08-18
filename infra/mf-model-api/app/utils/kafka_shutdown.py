"""
Graceful-shutdown utility for the asynchronous Kafka producer.
"""
import atexit
import signal
import sys
from app.mydb.ConnectUtil import kafka_client
from app.logs.stand_log import StandLogger


def graceful_shutdown():
    """Gracefully shut down the asynchronous Kafka producer."""
    if kafka_client is None:
        # No shutdown is needed when the metering backend is not Kafka.
        return
    try:
        StandLogger.info("开始优雅关闭Kafka异步生产者...")
        kafka_client.shutdown_async_producer()
        StandLogger.info("Kafka异步生产者已成功关闭")
    except Exception as e:
        StandLogger.error(f"关闭Kafka异步生产者时出错: {e}")


def signal_handler(signum, frame):
    """Signal handler."""
    StandLogger.info(f"收到信号 {signum}，开始优雅关闭...")
    graceful_shutdown()
    sys.exit(0)


def register_shutdown_handlers():
    """Register shutdown handlers."""
    # Register the cleanup function for process exit.
    atexit.register(graceful_shutdown)
    
    # Register signal handlers.
    signal.signal(signal.SIGINT, signal_handler)   # Ctrl+C
    signal.signal(signal.SIGTERM, signal_handler)  # Termination signal.
    
    StandLogger.info("Kafka优雅关闭处理器已注册")


# Automatically register shutdown handlers.
register_shutdown_handlers()
