"""Small asyncio Evidence producer adapter for the frozen Kafka record."""

import asyncio
import hashlib
import inspect
import json
import logging
import os
import re
import time
import uuid
from collections import deque
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Protocol

logger = logging.getLogger("bkn-agent.evidence.kafka")

TOPIC = "openbkn.evidence.v1"
PRODUCER_ID = "bkn-agent"
BASE_STREAM_ID = "bkn-agent"
WORKLOAD_IDENTITY = "bkn-agent"
MAX_UINT64 = (1 << 64) - 1


@dataclass(frozen=True)
class EvidenceKafkaConfig:
    bootstrap_servers: str
    username: str
    password: str
    capture_policy_revision: str
    queue_max_records: int = 4096
    queue_max_bytes: int = 64 << 20
    max_record_bytes: int = 1 << 20
    max_age_s: float = 30.0
    max_attempts: int = 5
    retry_backoff_s: float = 0.1
    shutdown_timeout_s: float = 5.0

    def validate(self) -> None:
        brokers = [part.strip() for part in self.bootstrap_servers.split(",")]
        valid_brokers = bool(brokers) and all(
            self._valid_broker(broker) for broker in brokers
        )
        if not valid_brokers or not self.username.strip() or not self.password:
            raise ValueError("invalid BKN Trace Evidence Kafka configuration")
        revision = self.capture_policy_revision
        if not re.fullmatch(r"[1-9][0-9]{0,19}", revision or "") or int(revision) > MAX_UINT64:
            raise ValueError("invalid BKN_TRACE_CAPTURE_POLICY_REVISION")
        if not 1 <= self.queue_max_records <= 1_000_000:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS")
        if not 1 <= self.queue_max_bytes <= 1 << 30:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES")
        if not 1 <= self.max_record_bytes <= 1 << 20:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES")
        if not 0.1 <= self.max_age_s <= 3600:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_MAX_AGE_S")
        if not 1 <= self.max_attempts <= 10:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_MAX_ATTEMPTS")
        if not 0 <= self.retry_backoff_s <= 60:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_RETRY_BACKOFF_MS")
        if not 0.1 <= self.shutdown_timeout_s <= 60:
            raise ValueError("invalid BKN_TRACE_EVIDENCE_DRAIN_TIMEOUT_S")

    @staticmethod
    def _valid_broker(broker: str) -> bool:
        if not broker:
            return False
        host, separator, port_text = broker.rpartition(":")
        if not separator or not host or not port_text.isdecimal():
            return False
        if host.startswith("[") and host.endswith("]"):
            host = host[1:-1]
        elif ":" in host:
            return False
        return bool(host) and 1 <= int(port_text) <= 65535

    @classmethod
    def from_env(cls) -> "EvidenceKafkaConfig":
        def integer(name: str, default: int) -> int:
            raw = os.getenv(name, str(default)).strip()
            try:
                return int(raw)
            except ValueError as exc:
                raise ValueError(f"invalid {name}") from exc

        def number(name: str, default: float) -> float:
            raw = os.getenv(name, str(default)).strip()
            try:
                return float(raw)
            except ValueError as exc:
                raise ValueError(f"invalid {name}") from exc

        result = cls(
            bootstrap_servers=os.getenv("BKN_TRACE_KAFKA_BROKERS", "").strip(),
            username=os.getenv("BKN_TRACE_KAFKA_USERNAME", "").strip(),
            password=os.getenv("BKN_TRACE_KAFKA_PASSWORD", ""),
            capture_policy_revision=os.getenv("BKN_TRACE_CAPTURE_POLICY_REVISION", "").strip(),
            queue_max_records=integer("BKN_TRACE_EVIDENCE_QUEUE_MAX_RECORDS", 4096),
            queue_max_bytes=integer("BKN_TRACE_EVIDENCE_QUEUE_MAX_BYTES", 64 << 20),
            max_record_bytes=integer("BKN_TRACE_EVIDENCE_MAX_RECORD_BYTES", 1 << 20),
            max_age_s=number("BKN_TRACE_EVIDENCE_MAX_AGE_S", 30),
            max_attempts=integer("BKN_TRACE_EVIDENCE_MAX_ATTEMPTS", 5),
            retry_backoff_s=number("BKN_TRACE_EVIDENCE_RETRY_BACKOFF_MS", 100) / 1000,
            shutdown_timeout_s=number("BKN_TRACE_EVIDENCE_DRAIN_TIMEOUT_S", 5),
        )
        result.validate()
        return result


@dataclass(frozen=True)
class KafkaRecord:
    topic: str
    key: bytes
    value: bytes
    headers: list[tuple[str, bytes]]


@dataclass(frozen=True)
class PublishResult:
    disposition: str
    event_id: str = ""
    reason: str = ""


@dataclass(frozen=True)
class DrainSummary:
    producer_instance_id: str
    capture_policy_revision: int
    last_accepted_sequence: int
    published: int
    dropped: int
    queue_empty: bool
    acknowledged_at: str


class Sender(Protocol):
    async def send(self, record: KafkaRecord) -> None: ...


def _canonical_json(value: Any) -> bytes:
    text = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return (
        text.replace("&", "\\u0026")
        .replace("<", "\\u003c")
        .replace(">", "\\u003e")
        .replace("\u2028", "\\u2028")
        .replace("\u2029", "\\u2029")
    ).encode("utf-8")


def build_record(
    event: dict[str, Any], config: EvidenceKafkaConfig, process_boot_id: str,
    sequence: int = 1,
) -> KafkaRecord:
    config.validate()
    if not isinstance(event, dict):
        raise ValueError("invalid_event")
    envelope = event.get("envelope")
    if not isinstance(envelope, dict) or not event.get("event_id") or not event.get("event_type"):
        raise ValueError("invalid_event")
    if not isinstance(envelope.get("owner"), dict):
        raise ValueError("invalid_event")
    stream_id = f"{BASE_STREAM_ID}:{process_boot_id}"
    instance_id = f"{WORKLOAD_IDENTITY}#{process_boot_id}"
    value: dict[str, Any] = {
        "event_id": event["event_id"],
        "event_type": event["event_type"],
        "bkn.trace.schema.version": "3.0.0",
        "payload_hash": hashlib.sha256(_canonical_json(envelope)).hexdigest(),
        "conversation_id": event.get("conversation_id", ""),
        "interaction_id": event.get("interaction_id", ""),
        "producer_id": PRODUCER_ID,
        "producer_stream_id": stream_id,
        "producer_epoch": 1,
        "producer_sequence": sequence,
        "started_at": event.get("started_at", ""),
        "observed_at": event.get("observed_at", ""),
        "emitted_at": event.get("emitted_at", ""),
        "envelope": envelope,
    }
    optional = ("operation_id", "attempt", "request_id", "trace_id", "span_id", "causation_event_ids", "artifact_refs", "business_refs", "operation_business_edges")
    for key in optional:
        candidate = event.get(key)
        if candidate not in (None, "", 0, [], {}):
            value[key] = candidate
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(raw) > config.max_record_bytes:
        raise ValueError("message_too_large")
    return KafkaRecord(
        topic=TOPIC,
        key=stream_id.encode("utf-8"),
        value=raw,
        headers=[
            ("content-type", b"application/json"),
            ("bkn-trace-schema-version", b"3.0.0"),
            ("capture_policy_revision", config.capture_policy_revision.encode("ascii")),
            ("producer_instance_id", instance_id.encode("utf-8")),
            ("bkn-evidence-record-class", b"live"),
        ],
    )


class AioKafkaSender:
    """Lazily starts aiokafka so broker unavailability never blocks app startup."""

    def __init__(self, config: EvidenceKafkaConfig):
        self.config = config
        self._producer = None
        self._start_lock = asyncio.Lock()

    async def _ensure_started(self):
        if self._producer is not None:
            return
        async with self._start_lock:
            if self._producer is not None:
                return
            from aiokafka import AIOKafkaProducer

            producer = AIOKafkaProducer(
                bootstrap_servers=[part.strip() for part in self.config.bootstrap_servers.split(",") if part.strip()],
                security_protocol="SASL_PLAINTEXT",
                sasl_mechanism="PLAIN",
                sasl_plain_username=self.config.username,
                sasl_plain_password=self.config.password,
                request_timeout_ms=1000,
                max_request_size=self.config.max_record_bytes,
                enable_idempotence=True,
                compression_type="snappy",
            )
            try:
                await asyncio.wait_for(producer.start(), timeout=1.0)
            except Exception:
                try:
                    await asyncio.wait_for(producer.stop(), timeout=1.0)
                except Exception:
                    pass
                raise
            self._producer = producer

    async def send(self, record: KafkaRecord) -> None:
        await self._ensure_started()
        await asyncio.wait_for(
            self._producer.send_and_wait(
                record.topic, record.value, key=record.key, headers=record.headers
            ),
            timeout=1.0,
        )

    async def close(self) -> None:
        if self._producer is not None:
            producer, self._producer = self._producer, None
            await producer.stop()


class EvidenceKafkaPublisher:
    def __init__(self, config: EvidenceKafkaConfig, sender: Sender | None = None):
        config.validate()
        self.config = config
        self.sender: Sender = sender or AioKafkaSender(config)
        self.process_boot_id = str(uuid.uuid4())
        self._queue: deque[tuple[KafkaRecord, int, float]] = deque()
        self._wake = asyncio.Event()
        self._worker: asyncio.Task | None = None
        self._outstanding_records = 0
        self._outstanding_bytes = 0
        self._sequence = 0
        self._published = 0
        self._dropped = 0
        self._closing = False

    @property
    def next_sequence(self) -> int:
        return self._sequence + 1

    def start(self) -> None:
        if self._worker is None:
            self._worker = asyncio.create_task(self._run())

    def try_publish(self, event: dict[str, Any]) -> PublishResult:
        if self._closing:
            return PublishResult("dropped", reason="publisher_closing")
        event_id = str(event.get("event_id") or "") if isinstance(event, dict) else ""
        try:
            candidate = build_record(event, self.config, self.process_boot_id, self._sequence + 1)
        except (TypeError, ValueError, UnicodeError) as exc:
            reason = str(exc) if str(exc) in {"invalid_event", "message_too_large"} else "serialization_failed"
            self._dropped += 1
            logger.warning("Evidence publish dropped reason=%s", reason)
            return PublishResult("dropped", event_id=event_id, reason=reason)
        size = len(candidate.key) + len(candidate.value) + sum(len(key) + len(value) for key, value in candidate.headers)
        if self._outstanding_records >= self.config.queue_max_records or self._outstanding_bytes + size > self.config.queue_max_bytes:
            self._dropped += 1
            logger.warning("Evidence publish dropped reason=queue_full")
            return PublishResult("dropped", event_id=event_id, reason="queue_full")
        self._sequence += 1
        self._outstanding_records += 1
        self._outstanding_bytes += size
        self._queue.append((candidate, size, time.monotonic()))
        self._wake.set()
        return PublishResult("accepted", event_id=event_id)

    async def _run(self) -> None:
        while not self._closing or self._queue:
            if not self._queue:
                self._wake.clear()
                if not self._closing:
                    await self._wake.wait()
                continue
            record, size, enqueued_at = self._queue.popleft()
            sent = False
            reason = "retry_exhausted"
            if time.monotonic() - enqueued_at > self.config.max_age_s:
                reason = "queue_timeout"
            else:
                for attempt in range(1, self.config.max_attempts + 1):
                    if time.monotonic() - enqueued_at > self.config.max_age_s:
                        reason = "queue_timeout"
                        break
                    try:
                        await self.sender.send(record)
                        sent = True
                        break
                    except asyncio.CancelledError:
                        raise
                    except Exception:
                        if attempt < self.config.max_attempts:
                            await asyncio.sleep(self.config.retry_backoff_s * attempt)
            if sent:
                self._published += 1
            else:
                self._dropped += 1
                logger.error("Evidence publish dropped reason=%s", reason)
            self._outstanding_records -= 1
            self._outstanding_bytes -= size

    async def close(self) -> DrainSummary:
        self._closing = True
        self._wake.set()
        if self._worker is not None:
            try:
                await asyncio.wait_for(self._worker, timeout=self.config.shutdown_timeout_s)
            except asyncio.TimeoutError:
                self._worker.cancel()
                try:
                    await self._worker
                except asyncio.CancelledError:
                    pass
                # Anything left after bounded shutdown is a terminal drop.
                self._dropped += self._outstanding_records
                self._outstanding_records = 0
                self._outstanding_bytes = 0
                self._queue.clear()
        close_sender = getattr(self.sender, "close", None)
        if close_sender is not None:
            result = close_sender()
            if inspect.isawaitable(result):
                try:
                    await asyncio.wait_for(result, timeout=self.config.shutdown_timeout_s)
                except asyncio.TimeoutError:
                    logger.error("Evidence publisher shutdown timed out reason=shutdown_timeout")
        return DrainSummary(
            producer_instance_id=f"{WORKLOAD_IDENTITY}#{self.process_boot_id}",
            capture_policy_revision=int(self.config.capture_policy_revision),
            last_accepted_sequence=self._sequence,
            published=self._published,
            dropped=max(0, self._sequence - self._published),
            queue_empty=self._outstanding_records == 0,
            acknowledged_at=datetime.now(timezone.utc).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
        )
