"""Internal Trace/Evidence policy validation for the BKN Agent publisher."""

import asyncio
import json
import logging
import os
from dataclasses import dataclass, replace
from datetime import datetime, timezone
from typing import Any, Callable
from urllib.parse import quote, urlparse

import aiohttp


logger = logging.getLogger("bkn-agent.evidence.policy")


_POLICY_FIELDS = (
    "contract_version", "revision", "trace_admission", "evidence_admission",
    "issued_at", "expires_at",
)


@dataclass(frozen=True)
class VerifiedPolicy:
    revision: int
    enabled: bool
    expires_at: datetime


class AckNotExpected(Exception):
    """This instance joined after the operation's frozen expected set."""


class TraceAdmissionClient:
    def __init__(
        self, policy_url: str, configuration_url: str, heartbeat_url: str,
        ack_url_base: str, *, request: Callable | None = None,
    ):
        values = (policy_url, configuration_url, heartbeat_url, ack_url_base)
        if any(urlparse(value).scheme not in ("http", "https") or not urlparse(value).netloc for value in values):
            raise ValueError("invalid Trace Admission endpoint")
        self.policy_url, self.configuration_url = policy_url, configuration_url
        self.heartbeat_url, self.ack_url_base = heartbeat_url, ack_url_base.rstrip("/")
        self.request = request or self._http_request

    @classmethod
    def from_env(cls) -> "TraceAdmissionClient":
        return cls(*(os.getenv(key, "").strip() for key in (
            "TRACE_ADMISSION_POLICY_URL", "TRACE_ADMISSION_CONFIGURATION_URL",
            "TRACE_ADMISSION_HEARTBEAT_URL", "TRACE_ADMISSION_ACK_URL_BASE")))

    @staticmethod
    async def _http_request(method: str, url: str, headers: dict, body: dict | None):
        timeout = aiohttp.ClientTimeout(total=5)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            async with session.request(method, url, headers=headers, json=body) as response:
                raw = await response.text()
                return response.status, json.loads(raw) if raw else {}

    async def _internal(self, method: str, url: str, body: dict | None = None):
        return await self.request(method, url, {"Content-Type": "application/json"}, body)

    async def read_policy(self) -> VerifiedPolicy:
        status, body = await self._internal("GET", self.policy_url)
        if status != 200:
            raise RuntimeError(f"Trace Admission policy status {status}")
        return verify_policy_snapshot(body)

    async def heartbeat(self, identity: str, boot_id: str, revision: int) -> None:
        status, _ = await self._internal("POST", self.heartbeat_url, {
            "endpoint_kind": "evidence_publisher",
            "workload_identity": identity,
            "instance_id": identity + "#" + boot_id,
            "process_boot_id": boot_id,
            "observed_revision": revision,
            "ready": True,
        })
        if not 200 <= status < 300:
            raise RuntimeError(f"Trace Admission heartbeat status {status}")

    async def operation_for_revision(self, revision: int) -> str | None:
        status, body = await self._internal("GET", self.configuration_url)
        if status != 200 or not isinstance(body, dict) or body.get("kind") != "configuration_get" or type(body.get("policy_revision")) is not int or body["policy_revision"] == 0:
            raise RuntimeError(f"Trace Admission configuration status {status}")
        if body["policy_revision"] != revision:
            return None
        operation = body.get("active_operation_id")
        return operation if isinstance(operation, str) and operation else None

    async def acknowledge(self, operation_id: str, summary: Any) -> None:
        status, body = await self._internal(
            "POST", self.ack_url_base + "/" + quote(operation_id, safe="") + ":publisher-ack", {
                "producer_instance_id": summary.producer_instance_id,
                "capture_policy_revision": summary.capture_policy_revision,
                "last_accepted_sequence": summary.last_accepted_sequence,
                "published": summary.published,
                "dropped": summary.dropped,
                "queue_empty": summary.queue_empty,
                "acknowledged_at": summary.acknowledged_at,
            },
        )
        if status == 409 and isinstance(body, dict) and body.get("code") == "EVIDENCE_PUBLISHER_ACK_NOT_EXPECTED":
            raise AckNotExpected()
        if not 200 <= status < 300:
            raise RuntimeError(f"Trace Admission ACK status {status}")


def verify_policy_snapshot(wire: dict, now: datetime | None = None) -> VerifiedPolicy:
    if not isinstance(wire, dict) or set(wire) != set(_POLICY_FIELDS):
        raise ValueError("invalid trace evidence policy fields")
    revision, mode = wire["revision"], wire["trace_admission"]
    if (wire["contract_version"] != "TraceEvidencePolicySnapshotV1"
        or type(revision) is not int or not 1 <= revision < 1 << 64
        or mode not in ("enabled", "disabled") or wire["evidence_admission"] != mode):
        raise ValueError("invalid trace evidence policy state")
    try:
        issued = datetime.fromisoformat(wire["issued_at"].replace("Z", "+00:00"))
        expires = datetime.fromisoformat(wire["expires_at"].replace("Z", "+00:00"))
    except (TypeError, ValueError, AttributeError) as exc:
        raise ValueError("invalid trace evidence policy snapshot") from exc
    clock = now or datetime.now(timezone.utc)
    if issued.tzinfo is None or expires.tzinfo is None or not issued <= clock < expires:
        raise ValueError("trace evidence policy snapshot expired")
    return VerifiedPolicy(revision=revision, enabled=mode == "enabled", expires_at=expires)


class EvidencePolicyRuntime:
    """One control loop around the existing bounded Kafka publisher."""

    def __init__(self, publisher: Any, control: Any):
        self.publisher = publisher
        self.control = control
        self._task: asyncio.Task | None = None
        self._policy: VerifiedPolicy | None = None
        self._acked: tuple[int, int] | None = None
        self._not_expected_revision: int | None = None

    def try_publish(self, event: dict):
        return self.publisher.try_publish(event)

    async def refresh(self) -> None:
        try:
            policy = await self.control.read_policy()
            same_enabled = (
                self._policy is not None and self._policy.revision == policy.revision
                and self._policy.enabled and policy.enabled and self.publisher._admitting
            )
            if not same_enabled:
                self.publisher.suspend_policy()
            self._policy = policy
            if not policy.enabled:
                self.publisher.apply_policy(policy)
            await self.control.heartbeat("bkn-agent", self.publisher.process_boot_id, policy.revision)
            operation = None
            if same_enabled:
                self.publisher.apply_policy(policy)
                if self._acked is not None and self._acked[0] == policy.revision or self._not_expected_revision == policy.revision:
                    return
                operation = await self.control.operation_for_revision(policy.revision)
                if not operation:
                    return
                self.publisher.suspend_policy()
            summary = await self.publisher.drain_for_revision(policy.revision)
            if not summary.queue_empty:
                raise RuntimeError("Evidence publisher queue disposition incomplete")
            if not same_enabled:
                operation = await self.control.operation_for_revision(policy.revision)
            if not policy.enabled and not operation and summary.last_accepted_sequence > 0 and self._acked != (policy.revision, summary.last_accepted_sequence):
                raise RuntimeError("no publisher acknowledgement candidate for queue disposition")
            if operation and self._acked != (policy.revision, summary.last_accepted_sequence) and self._not_expected_revision != policy.revision:
                try:
                    await self.control.acknowledge(operation, summary)
                    self._acked = (policy.revision, summary.last_accepted_sequence)
                except AckNotExpected:
                    if not policy.enabled:
                        raise
                    self._not_expected_revision = policy.revision
            if policy.enabled:
                self.publisher.apply_policy(policy)
        except Exception:
            self.publisher.suspend_policy()
            raise

    def start(self) -> None:
        self.publisher.start()
        self._task = asyncio.create_task(self.run())

    async def run(self) -> None:
        while True:
            try:
                await self.refresh()
            except asyncio.CancelledError:
                raise
            except Exception as exc:
                logger.warning("Evidence policy refresh failed: %s", exc)
            await asyncio.sleep(10)

    async def close(self):
        if self._task is not None:
            self._task.cancel()
            try:
                await self._task
            except asyncio.CancelledError:
                pass
        self.publisher.suspend_policy()
        summary = await self.publisher.close()
        policy = self._policy
        if policy is None:
            return summary
        summary = replace(summary, capture_policy_revision=policy.revision)
        if not summary.queue_empty:
            raise RuntimeError("Evidence publisher shutdown disposition incomplete")
        if self._acked == (policy.revision, summary.last_accepted_sequence) or self._not_expected_revision == policy.revision:
            return summary
        operation = await self.control.operation_for_revision(policy.revision)
        if operation:
            await self.control.acknowledge(operation, summary)
        elif not policy.enabled and summary.last_accepted_sequence > 0:
            raise RuntimeError("no publisher acknowledgement candidate for queue disposition")
        return summary
