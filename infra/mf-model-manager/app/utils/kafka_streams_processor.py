import json
import threading

from app.mydb.ConnectUtil import MyKafkaClient
from app.logs.stand_log import StandLogger

from app.utils.quota_aggregator import QuotaAggregator


class KafkaStreamsProcessor:
    def __init__(self, topic_name='tenant_a.dip.model_manager.quota_data', group_id='quota_data_group_new',
                 consume_from_beginning=False):
        # 使用新的消费者组ID，确保能够消费到所有消息
        self.kafka_client = MyKafkaClient(topic_name)
        self.topic_name = topic_name
        self.group_id = group_id
        self.consume_from_beginning = consume_from_beginning  # 是否从最早的消息开始消费
        # 聚合与落库逻辑抽到 QuotaAggregator，与 Redis Stream 消费端共用
        self.aggregator = QuotaAggregator()
        self.lock = threading.Lock()
        self.running = True  # 添加运行状态标志
        self.processed_messages = set()  # 用于记录已处理的消息，防止重复处理

    def _connect_consumer_with_custom_config(self):
        """使用自定义配置连接消费者"""
        from confluent_kafka import Consumer
        from app.core.config import base_config
        import socket

        # 使用固定的消费者组ID，确保多实例之间实现分区协作而不是重复消费
        hostname = socket.gethostname()
        import os
        pid = os.getpid()

        # 自定义消费者配置，避免重复消费
        offset_reset = 'earliest' if self.consume_from_beginning else 'latest'
        consumer_config = {
            'bootstrap.servers': '{}:{}'.format(base_config.KAFKAHOST, base_config.KAFKAPORT),
            'security.protocol': 'sasl_plaintext',
            'enable.ssl.certificate.verification': 'false',
            'sasl.mechanism': 'PLAIN',
            'sasl.username': base_config.KAFKAUSER,
            'sasl.password': base_config.KAFKAPASS,
            'group.id': self.group_id,  # 使用固定消费者组，避免多实例重复消费
            'auto.offset.reset': offset_reset,  # 根据配置决定从何处开始消费
            'enable.auto.commit': True,
            'auto.commit.interval.ms': 5000,  # 增加提交间隔
            'session.timeout.ms': 30000,  # 增加会话超时时间
            'heartbeat.interval.ms': 10000,  # 心跳间隔
            'max.poll.interval.ms': 300000,  # 最大轮询间隔
            'fetch.wait.max.ms': 500,  # 最大等待时间
            'fetch.min.bytes': 1,  # 最小字节数
            'fetch.max.bytes': 52428800  # 最大字节数
        }

        StandLogger.info_log(f"消费者配置: group.id={self.group_id}, auto.offset.reset={offset_reset}")
        StandLogger.info_log(f"主机名: {hostname}, 进程ID: {pid}")

        # 创建消费者
        self.kafka_client.consumer = Consumer(consumer_config)
        # 订阅topic
        self.kafka_client.consumer.subscribe([self.topic_name])

        StandLogger.info_log(f"消费者已订阅 Topic: {self.topic_name}")

    def start_consumer(self):
        """启动Kafka消费者"""
        StandLogger.info_log(f"启动Kafka消费者... Topic: {self.topic_name}, Group ID: {self.group_id}")

        try:
            StandLogger.info_log("正在连接Kafka消费者...")
            # 使用自定义配置连接消费者
            self._connect_consumer_with_custom_config()
            StandLogger.info_log("Kafka消费者连接成功")
        except Exception as e:
            StandLogger.error(f"连接Kafka消费者失败: {e}")
            raise

        # 启动定时落库任务（仅启动一次，聚合器内部防重）
        self.aggregator.start_periodic_flush()

        # 持续消费消息（批量）
        message_count = 0
        StandLogger.info_log("开始消费Kafka消息...")
        while self.running:
            try:
                batch = self.kafka_client.consume_batch(num_messages=500, timeout=0.2)
                if batch:
                    for message in batch:
                        message_count += 1
                        # StandLogger.info_log(
                        #     f"收到第{message_count}条消息: topic={message['topic']}, partition={message['partition']}, offset={message['offset']}")
                        self._process_message(message)
            except Exception as e:
                StandLogger.error(f"消费Kafka消息时出错: {e}")
                import time
                time.sleep(1)

        StandLogger.info_log("Kafka消费者已停止")

    def _process_message(self, message):
        """处理单条Kafka消息"""
        try:
            # 生成消息唯一标识符（基于topic、partition、offset）
            message_id = f"{message['topic']}_{message['partition']}_{message['offset']}"

            # 检查是否已经处理过此消息（幂等性检查）
            with self.lock:
                if message_id in self.processed_messages:
                    StandLogger.info_log(f"消息已处理过，跳过: {message_id}")
                    return
                self.processed_messages.add(message_id)

                # 限制已处理消息集合的大小，避免内存泄漏
                if len(self.processed_messages) > 10000:
                    # 保留最近5000个消息ID
                    self.processed_messages = set(list(self.processed_messages)[-5000:])

            # 解析消息内容
            value = message['value']
            if isinstance(value, bytes):
                value = value.decode('utf-8')

            data = json.loads(value)
            StandLogger.info_log(f"接收到消息: {data}")
            # 聚合（含计价与字段校验）
            self.aggregator.add_record(data)
        except json.JSONDecodeError as e:
            StandLogger.error(f"解析Kafka消息失败: {e}")
        except Exception as e:
            StandLogger.error(f"处理Kafka消息时出错: {e}")

    def stop_consumer(self):
        """停止Kafka消费者"""
        StandLogger.info_log("停止Kafka消费者...")
        self.running = False
        self.kafka_client.close_consumer()
        # 停止聚合器定时线程
        self.aggregator.stop()


# 全局实例
kafka_processor = None


def start_kafka_streams_processor():
    """启动Kafka Streams处理器"""
    global kafka_processor
    StandLogger.info_log("开始启动Kafka Streams处理器...")
    if kafka_processor is None:
        StandLogger.info_log("创建KafkaStreamsProcessor实例...")
        kafka_processor = KafkaStreamsProcessor()
        StandLogger.info_log("KafkaStreamsProcessor实例创建成功")

        StandLogger.info_log("开始调用start_consumer()方法...")
        # 直接运行消费者
        kafka_processor.start_consumer()
        StandLogger.info_log("Kafka Streams处理器已启动")
    else:
        StandLogger.info_log("KafkaStreamsProcessor实例已存在，跳过创建")
