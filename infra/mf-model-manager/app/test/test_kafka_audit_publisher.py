import json
import unittest

from app.utils import kafka_audit_publisher


class FakeProducer:
    def __init__(self):
        self.records = []
        self.fail = False
        self.error = None

    def produce(self, topic, *, key, value, headers, on_delivery):
        if self.fail:
            raise BufferError("queue full")
        if self.error:
            raise self.error
        self.records.append((topic, key, value, headers, on_delivery))

    def poll(self, timeout):
        return 0

    def flush(self, timeout):
        return 0


class KafkaAuditPublisherTest(unittest.TestCase):
    def test_enabled_publisher_requires_dedicated_static_configuration(self):
        with self.assertRaisesRegex(ValueError, "Audit environment"):
            kafka_audit_publisher.publisher_from_environment(
                {"MODEL_MANAGER_AUDIT_KAFKA_ENABLED": "true"},
                producer_factory=FakeProducer,
            )

    def test_enabled_publisher_uses_bounded_idempotent_ack_all_transport(self):
        captured = []

        def producer_factory(config):
            captured.append(config)
            return FakeProducer()

        publisher = kafka_audit_publisher.publisher_from_environment(
            {
                "MODEL_MANAGER_AUDIT_KAFKA_ENABLED": "true",
                "MODEL_MANAGER_AUDIT_ENVIRONMENT": "test",
                "MODEL_MANAGER_AUDIT_KAFKA_BROKERS": "kafka:9092",
                "MODEL_MANAGER_AUDIT_KAFKA_USERNAME": "model-manager-audit",
                "MODEL_MANAGER_AUDIT_KAFKA_PASSWORD": "test-secret",
            },
            producer_factory=producer_factory,
            start_polling=False,
        )

        self.assertIsNotNone(publisher)
        self.assertEqual(publisher.environment, "test")
        self.assertEqual(captured[0]["acks"], "all")
        self.assertTrue(captured[0]["enable.idempotence"])
        self.assertLessEqual(captured[0]["queue.buffering.max.messages"], 1024)
        self.assertEqual(captured[0]["sasl.username"], "model-manager-audit")

    def test_publish_uses_frozen_topic_key_header_and_bounded_value(self):
        producer = FakeProducer()
        publisher = kafka_audit_publisher.KafkaAuditPublisher(producer, start_polling=False)
        record = {
            "schema_version": "1.0", "source_id": "model-manager",
            "target": {"type": "llm_model", "id": "model-1"},
            "event_id": "d445bb9f-d3a3-4027-b0b0-61f1d34a776f",
        }

        self.assertTrue(publisher.publish(record))
        topic, key, value, headers, callback = producer.records[0]
        self.assertEqual(topic, "openbkn.audit.v1")
        self.assertEqual(key, b"model-manager\x1fllm_model\x1fmodel-1")
        self.assertEqual(headers, [("bkn-audit-schema-version", b"1.0")])
        self.assertEqual(json.loads(value), record)
        callback(None, None)
        self.assertEqual(publisher.delivered, 1)

    def test_queue_full_drops_audit_without_raising_into_business_handler(self):
        producer = FakeProducer()
        producer.fail = True
        publisher = kafka_audit_publisher.KafkaAuditPublisher(producer, start_polling=False)
        record = {
            "schema_version": "1.0", "source_id": "model-manager",
            "target": {"type": "llm_model", "id": "model-1"},
        }

        with self.assertLogs("app.utils.kafka_audit_publisher", level="ERROR"):
            self.assertFalse(publisher.publish(record))
        self.assertEqual(publisher.dropped, 1)

    def test_transport_exception_does_not_escape_into_business_handler(self):
        producer = FakeProducer()
        producer.error = RuntimeError("broker unavailable")
        publisher = kafka_audit_publisher.KafkaAuditPublisher(producer, start_polling=False)
        record = {
            "schema_version": "1.0", "source_id": "model-manager",
            "target": {"type": "llm_model", "id": "model-1"},
        }

        with self.assertLogs("app.utils.kafka_audit_publisher", level="ERROR"):
            self.assertFalse(publisher.publish(record))
        self.assertEqual(publisher.dropped, 1)


if __name__ == "__main__":
    unittest.main()
