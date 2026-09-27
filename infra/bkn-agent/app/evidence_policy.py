"""Signed Trace/Evidence policy verification for the BKN Agent publisher."""

import base64
import asyncio
import binascii
import json
import logging
import os
import time
from dataclasses import dataclass, replace
from datetime import datetime, timezone
from typing import Any, Callable
from urllib.parse import quote, urlparse

import aiohttp

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey

logger = logging.getLogger("bkn-agent.evidence.policy")


_SIGNED_FIELDS = (
    "contract_version", "revision", "trace_admission", "evidence_admission",
    "issued_at", "expires_at", "key_id", "audience_cluster_id",
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
        ack_url_base: str, token_url: str, client_id: str, client_secret: str,
        audience: str, key_id: str, public_key: bytes, *, scope: str = "",
        previous_key_id: str = "", previous_public_key: bytes = b"",
        request: Callable | None = None,
    ):
        values = (policy_url, configuration_url, heartbeat_url, ack_url_base, token_url)
        if any(urlparse(value).scheme not in ("http", "https") or not urlparse(value).netloc for value in values):
            raise ValueError("invalid Trace Admission endpoint")
        if not client_id or not client_secret or not audience or not key_id or len(public_key) != 32:
            raise ValueError("invalid Trace Admission identity or verifier")
        if bool(previous_key_id) != bool(previous_public_key) or (previous_public_key and len(previous_public_key) != 32):
            raise ValueError("invalid previous Trace Admission verifier")
        self.policy_url, self.configuration_url = policy_url, configuration_url
        self.heartbeat_url, self.ack_url_base = heartbeat_url, ack_url_base.rstrip("/")
        self.token_url, self.client_id, self.client_secret = token_url, client_id, client_secret
        self.audience, self.key_id, self.public_key = audience, key_id, public_key
        self.previous_key_id, self.previous_public_key = previous_key_id, previous_public_key
        self.scope = scope
        self.request = request or self._http_request
        self._access_token = ""
        self._token_expires_at = 0.0

    @classmethod
    def from_env(cls) -> "TraceAdmissionClient":
        def get(key: str) -> str:
            return os.getenv(key, "").strip()

        def decode_key(encoded: str) -> bytes:
            try:
                return base64.b64decode(encoded + "=" * (-len(encoded) % 4), validate=True)
            except (ValueError, binascii.Error) as exc:
                raise ValueError("invalid Trace Admission public key") from exc
        public_key = decode_key(get("TRACE_ADMISSION_CURRENT_PUBLIC_KEY"))
        previous_encoded = get("TRACE_ADMISSION_PREVIOUS_PUBLIC_KEY")
        previous_key = decode_key(previous_encoded) if previous_encoded else b""
        return cls(
            get("TRACE_ADMISSION_POLICY_URL"), get("TRACE_ADMISSION_CONFIGURATION_URL"),
            get("TRACE_ADMISSION_HEARTBEAT_URL"), get("TRACE_ADMISSION_ACK_URL_BASE"),
            get("TRACE_ADMISSION_TOKEN_URL"), get("TRACE_ADMISSION_CLIENT_ID"),
            os.getenv("TRACE_ADMISSION_CLIENT_SECRET", ""), get("TRACE_ADMISSION_AUDIENCE"),
            get("TRACE_ADMISSION_CURRENT_KEY_ID"), public_key, scope=get("TRACE_ADMISSION_SCOPE"),
            previous_key_id=get("TRACE_ADMISSION_PREVIOUS_KEY_ID"), previous_public_key=previous_key,
        )

    @staticmethod
    async def _http_request(method: str, url: str, headers: dict, body: dict | None):
        timeout = aiohttp.ClientTimeout(total=5)
        async with aiohttp.ClientSession(timeout=timeout) as session:
            form = headers.get("Content-Type") == "application/x-www-form-urlencoded"
            async with session.request(method, url, headers=headers, data=body if form else None, json=None if form else body) as response:
                raw = await response.text()
                return response.status, json.loads(raw) if raw else {}

    async def _token(self) -> str:
        if self._access_token and time.monotonic() < self._token_expires_at:
            return self._access_token
        body = {
            "grant_type": "client_credentials",
            "client_id": self.client_id,
            "client_secret": self.client_secret,
        }
        if self.scope:
            body["scope"] = self.scope
        status, result = await self.request("POST", self.token_url, {
            "Content-Type": "application/x-www-form-urlencoded",
        }, body)
        if status != 200 or not isinstance(result, dict) or not result.get("access_token"):
            raise RuntimeError(f"Trace Admission OAuth token status {status}")
        self._access_token = result["access_token"]
        self._token_expires_at = time.monotonic() + max(0, int(result.get("expires_in", 60)) - 30)
        return self._access_token

    async def _authorized(self, method: str, url: str, body: dict | None = None):
        token = await self._token()
        return await self.request(method, url, {"Authorization": "Bearer " + token, "Content-Type": "application/json"}, body)

    async def read_policy(self) -> VerifiedPolicy:
        status, body = await self._authorized("GET", self.policy_url)
        if status != 200:
            raise RuntimeError(f"Trace Admission policy status {status}")
        return verify_policy_snapshot(
            body, self.audience, self.key_id, self.public_key,
            previous_key_id=self.previous_key_id, previous_public_key=self.previous_public_key,
        )

    async def heartbeat(self, identity: str, boot_id: str, revision: int) -> None:
        status, _ = await self._authorized("POST", self.heartbeat_url, {
            "instance_id": identity + "#" + boot_id,
            "process_boot_id": boot_id,
            "observed_revision": revision,
            "ready": True,
        })
        if not 200 <= status < 300:
            raise RuntimeError(f"Trace Admission heartbeat status {status}")

    async def operation_for_revision(self, revision: int) -> str | None:
        status, body = await self._authorized("GET", self.configuration_url)
        if status != 200 or not isinstance(body, dict) or body.get("kind") != "configuration_get" or type(body.get("policy_revision")) is not int or body["policy_revision"] == 0:
            raise RuntimeError(f"Trace Admission configuration status {status}")
        if body["policy_revision"] != revision:
            return None
        operation = body.get("active_operation_id")
        return operation if isinstance(operation, str) and operation else None

    async def acknowledge(self, operation_id: str, summary: Any) -> None:
        status, body = await self._authorized(
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


def verify_policy_snapshot(
    wire: dict, audience: str, key_id: str, public_key: bytes,
    now: datetime | None = None,
    *, previous_key_id: str = "", previous_public_key: bytes = b"",
) -> VerifiedPolicy:
    if not isinstance(wire, dict) or set(wire) != {*_SIGNED_FIELDS, "signature"}:
        raise ValueError("invalid trace evidence policy fields")
    revision = wire["revision"]
    mode = wire["trace_admission"]
    if (
        wire["contract_version"] != "TraceEvidencePolicySnapshotV1"
        or type(revision) is not int or not 1 <= revision < 1 << 64
        or mode not in ("enabled", "disabled")
        or wire["evidence_admission"] != mode
        or wire["audience_cluster_id"] != audience
    ):
        raise ValueError("invalid trace evidence policy identity")
    if wire["key_id"] == key_id and len(public_key) == 32:
        selected_key = public_key
    elif wire["key_id"] == previous_key_id and previous_key_id and len(previous_public_key) == 32:
        selected_key = previous_public_key
    else:
        raise ValueError("invalid trace evidence policy key ID")
    try:
        issued = datetime.fromisoformat(wire["issued_at"].replace("Z", "+00:00"))
        expires = datetime.fromisoformat(wire["expires_at"].replace("Z", "+00:00"))
        signature_text = wire["signature"]
        if not isinstance(signature_text, str) or not signature_text.startswith("ed25519:"):
            raise ValueError("invalid trace evidence policy signature")
        encoded = signature_text[len("ed25519:"):]
        signature = base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
        if len(signature) != 64:
            raise ValueError("invalid trace evidence policy signature")
        canonical = json.dumps(
            {field: wire[field] for field in _SIGNED_FIELDS},
            separators=(",", ":"), ensure_ascii=False,
        )
        # Go encoding/json escapes these runes before the AO signs the wire.
        for char, escaped in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
            canonical = canonical.replace(char, escaped)
        canonical = canonical.encode("utf-8")
        Ed25519PublicKey.from_public_bytes(selected_key).verify(signature, canonical)
    except (TypeError, KeyError, ValueError) as exc:
        raise ValueError("invalid trace evidence policy snapshot") from exc
    except Exception as exc:
        raise ValueError("invalid trace evidence policy signature") from exc
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
