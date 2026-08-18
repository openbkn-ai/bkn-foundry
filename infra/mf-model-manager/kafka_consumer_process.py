#!/usr/bin/env python3
"""
Standalone metering consumer process launcher.

Starts the matching consumer for METERING_BACKEND:
- kafka: KafkaStreamsProcessor (existing behavior)
- redis: RedisStreamsProcessor (Redis Stream consumer group)
The kafka_consumer_process.py name is retained for compatibility with main.py's subprocess path.
"""
import os
import sys
import signal
import multiprocessing
from app.logs.stand_log import StandLogger
from app.core.config import base_config, resolve_metering_backend
from app.utils.config_cache import quota_config_cache_tree  # Initialize the configuration cache.


class MeteringConsumerProcess:
    def __init__(self):
        self.process = None
        self.backend = resolve_metering_backend()
        self.running = False

    def signal_handler(self, signum, frame):
        """Handle a signal by gracefully shutting down the metering consumer."""
        StandLogger.info_log(f"Received signal {signum}; gracefully shutting down the metering consumer...")
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
            StandLogger.error(f"Error stopping metering processor: {e}")

    def run_consumer(self):
        """Run the metering consumer."""
        try:
            StandLogger.info_log(f"Metering consumer process started, backend: {self.backend}")
            self.running = True

            # Register signal handlers.
            signal.signal(signal.SIGINT, self.signal_handler)
            signal.signal(signal.SIGTERM, self.signal_handler)
            StandLogger.info_log("Signal handlers registered")
            if self.backend == 'kafka':
                StandLogger.info_log("Importing Kafka Streams processor...")
                from app.utils.kafka_streams_processor import start_kafka_streams_processor
                StandLogger.info_log("Starting Kafka Streams processor...")
                start_kafka_streams_processor()
                StandLogger.info_log("Kafka Streams processor started")
            else:
                StandLogger.info_log("Importing Redis Streams processor...")
                from app.utils.redis_streams_processor import start_redis_streams_processor
                StandLogger.info_log("Starting Redis Streams processor...")
                start_redis_streams_processor()
                StandLogger.info_log("Redis Streams processor started")

        except Exception as e:
            StandLogger.error(f"Metering consumer process failed: {e}")
            import traceback
            StandLogger.error(f"Detailed error information: {traceback.format_exc()}")
            raise
        finally:
            StandLogger.info_log("Metering consumer process stopped")

    def start(self):
        """Start the metering consumer process."""
        try:
            # Run the consumer directly.
            self.run_consumer()
        except KeyboardInterrupt:
            StandLogger.info_log("Received keyboard interrupt; shutting down the metering consumer")
        except Exception as e:
            StandLogger.error(f"Failed to start metering consumer process: {e}")
            sys.exit(1)


# Preserve the legacy name used by main.py and external scripts.
KafkaConsumerProcess = MeteringConsumerProcess


def main():
    """Run the standalone metering consumer entry point."""
    StandLogger.info_log("=== Metering consumer process starting ===")  # Console output.
    StandLogger.info_log("Starting standalone metering consumer process")

    # Create and start the metering consumer.
    StandLogger.info_log("Creating MeteringConsumerProcess instance...")  # Console output.
    consumer_process = MeteringConsumerProcess()
    StandLogger.info_log("Starting consumer...")  # Console output.
    consumer_process.start()


if __name__ == '__main__':
    main()
