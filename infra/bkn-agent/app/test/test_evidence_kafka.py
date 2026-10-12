import json
import logging
from datetime import datetime, timedelta, timezone
from pathlib import Path

import pytest

from app import evidence, observability
from app.evidence_kafka import (
    EvidenceKafkaConfig,
    EvidenceKafkaPublisher,
    KafkaRecord,
    build_record,
)
from app.evidence_policy import VerifiedPolicy


FIXTURE = json.loads(
    (Path(__file__).parents[1] / "testdata/bkn_agent_evidence_record_v1.json").read_text()
)


def config(**overrides):
    values = {
        "bootstrap_servers": "kafka:9092",
        "username": "kafkauser",
        "password": "secret",
        "capture_policy_revision": "41",
        "queue_max_records": 2,
        "queue_max_bytes": 4096,
        "max_record_bytes": 1024 * 1024,
        "max_attempts": 3,
        "retry_backoff_s": 0,
        "shutdown_timeout_s": 1,
    }
    values.update(overrides)
    return EvidenceKafkaConfig(**values)


def test_build_record_matches_frozen_s1_record_shape_and_bkn_agent_identity():
    record = build_record(FIXTURE["event"], config(), FIXTURE["process_boot_id"])

    assert record.topic == FIXTURE["topic"]
    assert record.key.decode() == FIXTURE["record"]["key"]
    assert record.headers == [
        (header["key"], header["value"].encode())
        for header in FIXTURE["record"]["headers"]
    ]
    value = json.loads(record.value)
    assert value["producer_id"] == "bkn-agent"
    assert value["producer_stream_id"] == FIXTURE["record"]["key"]
    assert value["producer_epoch"] == 1
    assert value["producer_sequence"] == 1
    assert value["envelope"] == FIXTURE["event"]["envelope"]
    assert value["payload_hash"] == "eb4e31fcaad2332ef8f5dd1e4f52f0283c695b070e8ff007a3ffcf216ec04547"


@pytest.mark.anyio
async def test_start_publisher_uses_internal_policy_runtime(monkeypatch):
    configured = config()
    calls = []
    class FakePublisher:
        def __init__(self, publisher_config, *, policy_controlled):
            calls.append((publisher_config, policy_controlled))
    class FakeControl:
        @classmethod
        def from_env(cls):
            calls.append("control")
            return cls()
    class FakeRuntime:
        def __init__(self, publisher, control):
            calls.append((publisher, control))
        def start(self):
            calls.append("start")
        async def close(self):
            return type("Summary", (), {"published": 0, "dropped": 0, "queue_empty": True})()
    monkeypatch.setattr(evidence.EvidenceKafkaConfig, "from_env", lambda: configured)
    monkeypatch.setattr(evidence, "EvidenceKafkaPublisher", FakePublisher)
    monkeypatch.setattr(evidence, "TraceAdmissionClient", FakeControl, raising=False)
    monkeypatch.setattr(evidence, "EvidencePolicyRuntime", FakeRuntime, raising=False)
    await evidence.start_publisher()
    assert calls[0] == "control"
    assert calls[1] == (configured, True)
    assert calls[3] == "start"
    assert await evidence.drain_pending() is True


@pytest.mark.anyio
async def test_disabled_chart_does_not_start_evidence_publisher(monkeypatch):
    monkeypatch.setenv("BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED", "false")
    monkeypatch.setattr(evidence.EvidenceKafkaConfig, "from_env", lambda: (_ for _ in ()).throw(AssertionError("unexpected Kafka setup")))
    await evidence.start_publisher()
    assert evidence._publisher is None


@pytest.mark.anyio
@pytest.mark.parametrize("component", ["configuration", "control"])
async def test_publisher_initialization_failure_keeps_agent_available(monkeypatch, caplog, component):
    monkeypatch.setenv("BKN_TRACE_EVIDENCE_PUBLISHER_ENABLED", "true")
    def fail():
        raise ValueError("sensitive-test-password")
    monkeypatch.setattr(evidence.EvidenceKafkaConfig, "from_env", fail if component == "configuration" else lambda: config())
    monkeypatch.setattr(evidence.TraceAdmissionClient, "from_env", fail)
    with caplog.at_level(logging.ERROR):
        await evidence.start_publisher()
    assert evidence._publisher is None
    assert "Evidence publisher initialization failed" in caplog.text
    assert "sensitive-test-password" not in caplog.text
    assert await evidence.drain_pending() is True


class RecordingSender:
    def __init__(self, failures=0):
        self.failures = failures
        self.records = []

    async def send(self, record: KafkaRecord):
        if self.failures:
            self.failures -= 1
            raise RuntimeError("broker unavailable")
        self.records.append(record)


@pytest.mark.anyio
async def test_policy_controlled_publisher_stamps_verified_revision_and_closes_admission():
    publisher = EvidenceKafkaPublisher(config(), RecordingSender(), policy_controlled=True)
    publisher.start()
    assert publisher.try_publish(FIXTURE["event"]).reason == "publisher_unavailable"
    publisher.apply_policy(VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=1)))
    assert publisher.try_publish(FIXTURE["event"]).disposition == "accepted"
    queued = publisher._queue[0][0]
    assert dict(queued.headers)["capture_policy_revision"] == b"11"
    publisher.apply_policy(VerifiedPolicy(12, False, datetime.now(timezone.utc) + timedelta(minutes=1)))
    assert publisher.try_publish(FIXTURE["event"]).reason == "publisher_closing"
    await publisher.close()


@pytest.mark.anyio
async def test_queue_full_is_immediate_fail_open_and_does_not_consume_sequence():
    sender = RecordingSender()
    publisher = EvidenceKafkaPublisher(config(queue_max_records=1), sender)
    publisher.start()
    first = publisher.try_publish(FIXTURE["event"])
    second_event = {**FIXTURE["event"], "event_id": "evt-second"}
    second = publisher.try_publish(second_event)

    assert first.disposition == "accepted"
    assert second.disposition == "dropped"
    assert second.reason == "queue_full"
    assert publisher.next_sequence == 2
    await publisher.close()


@pytest.mark.anyio
async def test_retry_exhaustion_is_observable_but_does_not_raise_to_caller():
    sender = RecordingSender(failures=3)
    publisher = EvidenceKafkaPublisher(config(), sender)
    publisher.start()
    result = publisher.try_publish(FIXTURE["event"])
    summary = await publisher.close()

    assert result.disposition == "accepted"
    assert summary.published == 0
    assert summary.dropped == 1
    assert summary.queue_empty is True


@pytest.mark.anyio
async def test_kafka_ack_does_not_upgrade_core_lifecycle_durability(monkeypatch):
    sender = RecordingSender()
    publisher = EvidenceKafkaPublisher(config(), sender)
    publisher.start()
    monkeypatch.setattr(evidence, "_publisher", publisher, raising=False)
    context_token, interaction_token = _interaction()
    try:
        current = evidence._interaction.get()
        event = current.started_event
        assert await evidence.submit_events([event], "user-1", "user") is True
        assert event["event_id"] in current.locally_admitted_event_ids
        assert not hasattr(current, "ledger_durable_event_ids")

        ack = await publisher.close()

        assert ack.published == 1
        assert ack.queue_empty is True
        assert event["event_id"] in current.locally_admitted_event_ids
        assert not hasattr(current, "ledger_durable_event_ids")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(context_token)


class ImmediatePublisher:
    def __init__(self, disposition):
        self.disposition = disposition

    def try_publish(self, event):
        from app.evidence_kafka import PublishResult

        return PublishResult(self.disposition, event_id=event["event_id"], reason="queue_full" if self.disposition == "dropped" else "")


def _interaction():
    context = observability.build_context({
        "bkn-request-id": "req-evidence-kafka-test",
        "x-account-id": "user-1",
        "x-account-type": "user",
        "x-bkn-application-principal-id": "openbkn-studio",
        "x-bkn-effective-subject-type": "user",
        "x-bkn-effective-subject-id": "user-1",
    })
    context_token = observability.set_context(context)
    interaction_token = evidence.begin_interaction(
        "question", "chat", "agent-1", "bkn.agent.chat",
        conversation_id="conv-1", interaction_id="int-1",
    )
    return context_token, interaction_token


@pytest.mark.anyio
async def test_queue_acceptance_sets_local_admission_without_claiming_ledger_durability(monkeypatch):
    monkeypatch.setattr(evidence, "_publisher", ImmediatePublisher("accepted"), raising=False)
    context_token, interaction_token = _interaction()
    try:
        current = evidence._interaction.get()
        event = current.started_event
        assert await evidence.submit_events([event], "user-1", "user") is True
        assert event["event_id"] in current.locally_admitted_event_ids
        assert not hasattr(current, "ledger_durable_event_ids")
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(context_token)


@pytest.mark.anyio
async def test_policy_denied_batch_records_coverage_gap_without_payload(monkeypatch, caplog):
    from app.evidence_kafka import PublishResult

    class DeniedPublisher:
        def try_publish(self, event):
            return PublishResult("dropped", event_id=event["event_id"], reason="publisher_unavailable")

    monkeypatch.setattr(evidence, "_publisher", DeniedPublisher(), raising=False)
    context_token, interaction_token = _interaction()
    try:
        event = evidence._interaction.get().started_event
        with caplog.at_level(logging.WARNING, logger="bkn-agent.evidence"):
            assert await evidence.submit_events([event], "user-1", "user") is False
        assert "bkn_trace_coverage_gap" in caplog.text
        assert "publisher_unavailable" in caplog.text
        assert event["event_id"] not in caplog.text
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(context_token)


@pytest.mark.anyio
async def test_queue_full_does_not_add_causation_but_submit_remains_fail_open(monkeypatch):
    monkeypatch.setattr(evidence, "_publisher", ImmediatePublisher("dropped"), raising=False)
    context_token, interaction_token = _interaction()
    try:
        current = evidence._interaction.get()
        event = current.started_event
        assert await evidence.submit_events([event], "user-1", "user") is False
        assert event["event_id"] not in current.locally_admitted_event_ids
        _, parent_event_id = evidence.new_operation()
        assert parent_event_id is None
    finally:
        evidence.end_interaction(interaction_token)
        observability.reset_context(context_token)


@pytest.mark.parametrize(
    "overrides",
    [
        {"bootstrap_servers": ""},
        {"bootstrap_servers": "kafka"},
        {"bootstrap_servers": "kafka:0"},
        {"bootstrap_servers": "kafka:9092,"},
        {"username": ""},
        {"password": ""},
        {"capture_policy_revision": "0"},
        {"capture_policy_revision": "041"},
        {"queue_max_records": 0},
        {"max_attempts": 0},
    ],
)
def test_invalid_or_missing_kafka_config_is_rejected(overrides):
    with pytest.raises(ValueError):
        config(**overrides).validate()


def test_from_env_requires_bootstrap_credentials_but_not_static_revision(monkeypatch):
    for name in (
        "BKN_TRACE_KAFKA_BROKERS",
        "BKN_TRACE_KAFKA_USERNAME",
        "BKN_TRACE_KAFKA_PASSWORD",
        "BKN_TRACE_CAPTURE_POLICY_REVISION",
    ):
        monkeypatch.delenv(name, raising=False)
    with pytest.raises(ValueError):
        EvidenceKafkaConfig.from_env()
    monkeypatch.setenv("BKN_TRACE_KAFKA_BROKERS", "kafka:9092")
    monkeypatch.setenv("BKN_TRACE_KAFKA_USERNAME", "agent")
    monkeypatch.setenv("BKN_TRACE_KAFKA_PASSWORD", "test-password")
    assert EvidenceKafkaConfig.from_env().capture_policy_revision == "1"


@pytest.mark.anyio
async def test_retry_stops_when_record_exceeds_max_age(monkeypatch):
    now = [10.0]

    class SlowFailingSender(RecordingSender):
        calls = 0

        async def send(self, record):
            self.calls += 1
            now[0] += 0.2
            raise RuntimeError("broker unavailable")

    sender = SlowFailingSender(failures=10)
    publisher = EvidenceKafkaPublisher(config(max_attempts=10, max_age_s=0.1), sender)
    monkeypatch.setattr("app.evidence_kafka.time.monotonic", lambda: now[0])
    publisher.start()
    assert publisher.try_publish(FIXTURE["event"]).disposition == "accepted"
    summary = await publisher.close()
    assert summary.dropped == 1
    assert sender.calls == 1


def test_evidence_path_has_no_http_sender_or_http_fallback():
    assert not hasattr(evidence, "_send_once")
    assert not hasattr(evidence.config, "BKN_TRACE_EVIDENCE_INGEST_URL")
