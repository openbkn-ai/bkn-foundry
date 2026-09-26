import unittest

from kafka_ack import publish_with_ack
from manifest import ManifestError


class Metadata:
    topic, partition, offset = "openbkn.evidence.v1", 2, 41


class Future:
    def get(self, timeout):
        self.timeout = timeout
        return Metadata()


class Producer:
    def send(self, topic, **kwargs):
        self.topic, self.kwargs = topic, kwargs
        return Future()


class KafkaTest(unittest.TestCase):
    def test_publishes_exact_key_value_headers_and_returns_ack(self):
        producer = Producer()
        record = {"key": "stream", "value": b'{"event_id":"evt"}', "headers": {"content-type": "application/json", "capture_policy_revision": "0"}}
        self.assertEqual(publish_with_ack(producer, "openbkn.evidence.v1", record, 5), {"topic": "openbkn.evidence.v1", "partition": 2, "offset": 41})
        self.assertEqual(producer.topic, "openbkn.evidence.v1")
        self.assertEqual(producer.kwargs["key"], b"stream")
        self.assertEqual(producer.kwargs["value"], record["value"])
        self.assertEqual(producer.kwargs["headers"], [("content-type", b"application/json"), ("capture_policy_revision", b"0")])

    def test_rejects_invalid_ack(self):
        class BadProducer:
            def send(self, *_args, **_kwargs):
                return type("Future", (), {"get": lambda self, timeout: type("Metadata", (), {"topic": "wrong", "partition": 0, "offset": 0})()})()
        with self.assertRaises(ManifestError):
            publish_with_ack(BadProducer(), "openbkn.evidence.v1", {"key": "stream", "value": b"{}", "headers": {}}, 5)


if __name__ == "__main__":
    unittest.main()
