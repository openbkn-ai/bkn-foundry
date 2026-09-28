"""Bounded, fail-open transport for Model Manager Audit v1 records."""

import json
import logging
import threading

logger = logging.getLogger(__name__)

_TOPIC = "openbkn.audit.v1"
_SCHEMA_HEADER = "bkn-audit-schema-version"
_MAX_VALUE_BYTES = 32 * 1024


def publisher_from_environment(environment, *, producer_factory=None, start_polling=True):
    """Construct a dedicated Audit producer; invalid enabled config fails startup."""
    enabled = environment.get("MODEL_MANAGER_AUDIT_KAFKA_ENABLED", "false").lower()
    if enabled == "false":
        return None
    if enabled != "true":
        raise ValueError("invalid Audit enabled value")
    audit_environment = environment.get("MODEL_MANAGER_AUDIT_ENVIRONMENT", "")
    if audit_environment not in {"development", "test", "staging", "production"}:
        raise ValueError("invalid Audit environment")
    brokers = environment.get("MODEL_MANAGER_AUDIT_KAFKA_BROKERS", "").strip()
    username = environment.get("MODEL_MANAGER_AUDIT_KAFKA_USERNAME", "").strip()
    password = environment.get("MODEL_MANAGER_AUDIT_KAFKA_PASSWORD", "")
    if not brokers or not username or not password:
        raise ValueError("missing Audit Kafka configuration")
    protocol = environment.get("MODEL_MANAGER_AUDIT_KAFKA_SECURITY_PROTOCOL", "SASL_PLAINTEXT")
    if protocol not in {"SASL_PLAINTEXT", "SASL_SSL"}:
        raise ValueError("invalid Audit Kafka security protocol")
    mechanism = environment.get("MODEL_MANAGER_AUDIT_KAFKA_SASL_MECHANISM", "PLAIN")
    if mechanism != "PLAIN":
        raise ValueError("invalid Audit Kafka SASL mechanism")
    if producer_factory is None:
        from confluent_kafka import Producer

        producer_factory = Producer
    producer = producer_factory({
        "bootstrap.servers": brokers,
        "security.protocol": protocol,
        "sasl.mechanism": mechanism,
        "sasl.username": username,
        "sasl.password": password,
        "acks": "all",
        "enable.idempotence": True,
        "retries": 3,
        "message.timeout.ms": 10000,
        "queue.buffering.max.messages": 512,
        "queue.buffering.max.kbytes": 1024,
        "linger.ms": 5,
    })
    return KafkaAuditPublisher(producer, environment=audit_environment, start_polling=start_polling)


class KafkaAuditPublisher:
    def __init__(self, producer, *, environment="test", start_polling=True):
        self._producer = producer
        self.environment = environment
        self._stop = threading.Event()
        self._lock = threading.Lock()
        self.accepted = 0
        self.delivered = 0
        self.failed = 0
        self.dropped = 0
        self._poller = None
        if start_polling:
            self._poller = threading.Thread(target=self._poll, name="model-manager-audit-kafka", daemon=True)
            self._poller.start()

    def publish(self, record):
        try:
            if record.get("source_id") != "model-manager" or record.get("schema_version") != "1.0":
                raise ValueError("invalid Audit source or schema version")
            target = record["target"]
            key = "\x1f".join(("model-manager", target["type"], target["id"])).encode("utf-8")
            value = json.dumps(record, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
            if len(value) > _MAX_VALUE_BYTES:
                raise ValueError("Audit value exceeds limit")
            self._producer.produce(
                _TOPIC,
                key=key,
                value=value,
                headers=[(_SCHEMA_HEADER, b"1.0")],
                on_delivery=self._on_delivery,
            )
        except Exception as error:
            with self._lock:
                self.dropped += 1
            logger.error("model_manager_audit_enqueue_failed error_type=%s", type(error).__name__)
            return False
        with self._lock:
            self.accepted += 1
        return True

    def _on_delivery(self, error, _message):
        with self._lock:
            if error is None:
                self.delivered += 1
            else:
                self.failed += 1
        if error is not None:
            logger.error("model_manager_audit_delivery_failed error_type=%s", type(error).__name__)

    def _poll(self):
        while not self._stop.is_set():
            try:
                self._producer.poll(0.1)
            except Exception:
                logger.error("model_manager_audit_poll_failed", exc_info=True)
                self._stop.wait(0.1)

    def close(self):
        self._stop.set()
        if self._poller is not None:
            self._poller.join(timeout=1)
        try:
            pending = self._producer.flush(5)
        except Exception:
            logger.error("model_manager_audit_flush_failed", exc_info=True)
            return
        if pending:
            logger.error("model_manager_audit_shutdown_pending count=%d", pending)
