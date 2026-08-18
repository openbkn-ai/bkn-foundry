import json
import threading

from app.mydb.ConnectUtil import MyKafkaClient
from app.logs.stand_log import StandLogger

from app.utils.quota_aggregator import QuotaAggregator


class KafkaStreamsProcessor:
    def __init__(self, topic_name='tenant_a.dip.model_manager.quota_data', group_id='quota_data_group_new',
                 consume_from_beginning=False):
        self.kafka_client = MyKafkaClient(topic_name)
        self.topic_name = topic_name
        self.group_id = group_id
        self.consume_from_beginning = consume_from_beginning  # Whether to consume from the earliest offset.
        self.aggregator = QuotaAggregator()
        self.lock = threading.Lock()
        self.running = True  # Track the running state.
        self.processed_messages = set()  # Track processed messages to prevent duplicates.

    def _connect_consumer_with_custom_config(self):
        """Connect the consumer with custom configuration."""
        from confluent_kafka import Consumer
        from app.core.config import base_config
        import socket

        hostname = socket.gethostname()
        import os
        pid = os.getpid()

        offset_reset = 'earliest' if self.consume_from_beginning else 'latest'
        consumer_config = {
            'bootstrap.servers': '{}:{}'.format(base_config.KAFKAHOST, base_config.KAFKAPORT),
            'security.protocol': 'sasl_plaintext',
            'enable.ssl.certificate.verification': 'false',
            'sasl.mechanism': 'PLAIN',
            'sasl.username': base_config.KAFKAUSER,
            'sasl.password': base_config.KAFKAPASS,
            'group.id': self.group_id,  # Use a fixed consumer group to avoid duplicate consumption.
            'auto.offset.reset': offset_reset,  # Choose the starting offset from configuration.
            'enable.auto.commit': True,
            'auto.commit.interval.ms': 5000,  # Increase the commit interval.
            'session.timeout.ms': 30000,  # Increase the session timeout.
            'heartbeat.interval.ms': 10000,  # Heartbeat interval.
            'max.poll.interval.ms': 300000,  # Maximum poll interval.
            'fetch.wait.max.ms': 500,  # Maximum wait time.
            'fetch.min.bytes': 1,  # Minimum bytes.
            'fetch.max.bytes': 52428800  # Maximum bytes.
        }

        StandLogger.info_log(f"消费者配置: group.id={self.group_id}, auto.offset.reset={offset_reset}")
        StandLogger.info_log(f"主机名: {hostname}, 进程ID: {pid}")

        self.kafka_client.consumer = Consumer(consumer_config)
        self.kafka_client.consumer.subscribe([self.topic_name])

        StandLogger.info_log(f"消费者已订阅 Topic: {self.topic_name}")

    def start_consumer(self):
        """Start the Kafka consumer."""
        StandLogger.info_log(f"启动Kafka消费者... Topic: {self.topic_name}, Group ID: {self.group_id}")

        try:
            StandLogger.info_log("正在连接Kafka消费者...")
            self._connect_consumer_with_custom_config()
            StandLogger.info_log("Kafka消费者连接成功")
        except Exception as e:
            StandLogger.error(f"连接Kafka消费者失败: {e}")
            raise

        self.aggregator.start_periodic_flush()

        message_count = 0
        StandLogger.info_log("开始消费Kafka消息...")
        while self.running:
            try:
                batch = self.kafka_client.consume_batch(num_messages=500, timeout=0.2)
                if batch:
                    for message in batch:
                        message_count += 1
                        # StandLogger.info_log(
                        self._process_message(message)
            except Exception as e:
                StandLogger.error(f"消费Kafka消息时出错: {e}")
                import time
                time.sleep(1)

        StandLogger.info_log("Kafka消费者已停止")

    def _process_message(self, message):
        """Process one Kafka message."""
        try:
            message_id = f"{message['topic']}_{message['partition']}_{message['offset']}"

            with self.lock:
                if message_id in self.processed_messages:
                    StandLogger.info_log(f"消息已处理过，跳过: {message_id}")
                    return
                self.processed_messages.add(message_id)

                if len(self.processed_messages) > 10000:
                    self.processed_messages = set(list(self.processed_messages)[-5000:])

            value = message['value']
            if isinstance(value, bytes):
                value = value.decode('utf-8')

            data = json.loads(value)
            StandLogger.info_log(f"接收到消息: {data}")
            self.aggregator.add_record(data)
        except json.JSONDecodeError as e:
            StandLogger.error(f"解析Kafka消息失败: {e}")
        except Exception as e:
            StandLogger.error(f"处理Kafka消息时出错: {e}")

    def stop_consumer(self):
        """Stop the Kafka consumer."""
        StandLogger.info_log("停止Kafka消费者...")
        self.running = False
        self.kafka_client.close_consumer()
        self.aggregator.stop()


kafka_processor = None


def start_kafka_streams_processor():
    """Run the Kafka Streams processor."""
    global kafka_processor
    StandLogger.info_log("开始启动Kafka Streams处理器...")
    if kafka_processor is None:
        StandLogger.info_log("创建KafkaStreamsProcessor实例...")
        kafka_processor = KafkaStreamsProcessor()
        StandLogger.info_log("KafkaStreamsProcessor实例创建成功")

        StandLogger.info_log("开始调用start_consumer()方法...")
        kafka_processor.start_consumer()
        StandLogger.info_log("Kafka Streams处理器已启动")
    else:
        StandLogger.info_log("KafkaStreamsProcessor实例已存在，跳过创建")
