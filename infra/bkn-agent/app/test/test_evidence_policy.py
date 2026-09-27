import base64
import json
from datetime import datetime, timedelta, timezone

import pytest
from aiohttp import web
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from app.evidence_policy import AckNotExpected, EvidencePolicyRuntime, TraceAdmissionClient, VerifiedPolicy, verify_policy_snapshot
from app.evidence_kafka import DrainSummary, EvidenceKafkaConfig, EvidenceKafkaPublisher


def signed_policy(private_key, *, revision=11, mode="enabled", audience="cluster-a"):
    now = datetime.now(timezone.utc)
    fields = {
        "contract_version": "TraceEvidencePolicySnapshotV1",
        "revision": revision,
        "trace_admission": mode,
        "evidence_admission": mode,
        "issued_at": (now - timedelta(seconds=2)).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
        "expires_at": (now + timedelta(minutes=5)).isoformat(timespec="milliseconds").replace("+00:00", "Z"),
        "key_id": "test-key",
        "audience_cluster_id": audience,
    }
    canonical = json.dumps(fields, separators=(",", ":"), ensure_ascii=False)
    for char, escaped in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"), ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        canonical = canonical.replace(char, escaped)
    canonical = canonical.encode()
    fields["signature"] = "ed25519:" + base64.urlsafe_b64encode(private_key.sign(canonical)).decode().rstrip("=")
    return fields


def test_verified_policy_uses_signed_revision_and_mode():
    private_key = Ed25519PrivateKey.generate()
    public_key = private_key.public_key().public_bytes_raw()
    snapshot = verify_policy_snapshot(signed_policy(private_key), "cluster-a", "test-key", public_key)
    assert snapshot.revision == 11
    assert snapshot.enabled is True


def test_verified_policy_accepts_go_json_escaped_signed_fields():
    private_key = Ed25519PrivateKey.generate()
    audience = "cluster<&>\u2028"
    snapshot = verify_policy_snapshot(
        signed_policy(private_key, audience=audience), audience,
        "test-key", private_key.public_key().public_bytes_raw(),
    )
    assert snapshot.revision == 11


@pytest.mark.parametrize("change", [
    {"revision": 12},
    {"evidence_admission": "disabled"},
    {"audience_cluster_id": "other-cluster"},
    {"key_id": "unknown"},
])
def test_policy_rejects_tampering_and_identity_mismatch(change):
    private_key = Ed25519PrivateKey.generate()
    public_key = private_key.public_key().public_bytes_raw()
    wire = signed_policy(private_key)
    wire.update(change)
    with pytest.raises(ValueError):
        verify_policy_snapshot(wire, "cluster-a", "test-key", public_key)


def test_verified_policy_accepts_configured_previous_key_only():
    current = Ed25519PrivateKey.generate()
    previous = Ed25519PrivateKey.generate()
    wire = signed_policy(previous)
    wire["key_id"] = "previous-key"
    fields = {key: wire[key] for key in (
        "contract_version", "revision", "trace_admission", "evidence_admission",
        "issued_at", "expires_at", "key_id", "audience_cluster_id",
    )}
    canonical = json.dumps(fields, separators=(",", ":"), ensure_ascii=False).encode()
    wire["signature"] = "ed25519:" + base64.urlsafe_b64encode(previous.sign(canonical)).decode().rstrip("=")
    assert verify_policy_snapshot(
        wire, "cluster-a", "test-key", current.public_key().public_bytes_raw(),
        previous_key_id="previous-key", previous_public_key=previous.public_key().public_bytes_raw(),
    ).revision == 11


class FakeControl:
    def __init__(self, policy):
        self.policy = policy
        self.heartbeats = []
        self.acks = []
        self.fail = False

    async def read_policy(self):
        if self.fail:
            raise RuntimeError("control unavailable")
        return self.policy

    async def heartbeat(self, identity, boot_id, revision):
        self.heartbeats.append((identity, boot_id, revision))

    async def operation_for_revision(self, revision):
        return f"op-{revision}"

    async def acknowledge(self, operation_id, summary):
        self.acks.append((operation_id, summary))


@pytest.mark.anyio
async def test_runtime_registers_each_revision_and_fails_closed_on_control_error():
    config = EvidenceKafkaConfig("kafka:9092", "agent", "test-password", "1")
    class Sender:
        async def send(self, record):
            pass
    publisher = EvidenceKafkaPublisher(config, Sender(), policy_controlled=True)
    publisher.start()
    control = FakeControl(VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=1)))
    runtime = EvidencePolicyRuntime(publisher, control)
    assert runtime.try_publish({"event_id": "before"}).disposition == "dropped"
    await runtime.refresh()
    assert control.heartbeats[-1][2] == 11
    assert control.acks[-1][0] == "op-11"
    assert publisher._admitting is True
    control.policy = VerifiedPolicy(12, False, datetime.now(timezone.utc) + timedelta(minutes=1))
    await runtime.refresh()
    assert control.heartbeats[-1][2] == 12
    assert control.acks[-1][0] == "op-12"
    assert publisher._admitting is False
    control.fail = True
    with pytest.raises(RuntimeError):
        await runtime.refresh()
    assert publisher._admitting is False
    await publisher.close()


@pytest.mark.anyio
async def test_late_joining_instance_can_admit_after_verified_policy_and_heartbeat():
    config = EvidenceKafkaConfig("kafka:9092", "agent", "test-password", "1")
    class Sender:
        async def send(self, record):
            pass
    class LateJoinControl(FakeControl):
        async def acknowledge(self, operation_id, summary):
            raise AckNotExpected()
    publisher = EvidenceKafkaPublisher(config, Sender(), policy_controlled=True)
    publisher.start()
    control = LateJoinControl(VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=1)))
    await EvidencePolicyRuntime(publisher, control).refresh()
    assert control.heartbeats[-1][2] == 11
    assert publisher._admitting is True
    await publisher.close()


@pytest.mark.anyio
async def test_stable_enabled_refresh_keeps_admission_with_inflight_queue():
    config = EvidenceKafkaConfig("kafka:9092", "agent", "test-password", "1")
    class Sender:
        async def send(self, record):
            pass
    publisher = EvidenceKafkaPublisher(config, Sender(), policy_controlled=True)
    publisher.start()
    control = FakeControl(VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=1)))
    runtime = EvidencePolicyRuntime(publisher, control)
    await runtime.refresh()
    async def unfinished_drain(revision):
        return DrainSummary("bkn-agent#boot", revision, 1, 0, 0, False, "2026-09-27T00:00:00.000Z")
    publisher.drain_for_revision = unfinished_drain
    refreshed_policy = VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=5))
    control.policy = refreshed_policy
    await runtime.refresh()
    assert publisher._admitting is True
    assert publisher._policy is refreshed_policy
    assert len(control.heartbeats) == 2
    assert len(control.acks) == 1
    await publisher.close()


@pytest.mark.anyio
async def test_disabled_revision_without_ack_candidate_keeps_unaccounted_queue_closed():
    config = EvidenceKafkaConfig("kafka:9092", "agent", "test-password", "1")
    class Sender:
        async def send(self, record):
            pass
    class NoCandidate(FakeControl):
        async def operation_for_revision(self, revision):
            return None
    publisher = EvidenceKafkaPublisher(config, Sender(), policy_controlled=True)
    publisher.start()
    control = NoCandidate(VerifiedPolicy(12, False, datetime.now(timezone.utc) + timedelta(minutes=1)))
    runtime = EvidencePolicyRuntime(publisher, control)
    publisher._sequence = 1
    with pytest.raises(RuntimeError, match="acknowledgement candidate"):
        await runtime.refresh()
    assert publisher._admitting is False
    await publisher.close()


@pytest.mark.anyio
async def test_close_acknowledges_final_disposition_at_verified_revision():
    config = EvidenceKafkaConfig("kafka:9092", "agent", "test-password", "1")
    class Sender:
        async def send(self, record):
            pass
    publisher = EvidenceKafkaPublisher(config, Sender(), policy_controlled=True)
    control = FakeControl(VerifiedPolicy(11, True, datetime.now(timezone.utc) + timedelta(minutes=1)))
    runtime = EvidencePolicyRuntime(publisher, control)
    runtime.start()
    await runtime.refresh()
    publisher._sequence = 1
    summary = await runtime.close()
    assert summary.capture_policy_revision == 11
    assert control.acks[-1][1].capture_policy_revision == 11
    assert control.acks[-1][1].last_accepted_sequence == 1
    assert control.acks[-1][1].dropped == 1


@pytest.mark.anyio
async def test_control_client_uses_oauth_and_frozen_heartbeat_ack_wires():
    private_key = Ed25519PrivateKey.generate()
    requests = []
    async def request(method, url, headers, body):
        requests.append((method, url, headers, body))
        if url.endswith("/oauth2/token"):
            assert body == {"grant_type": "client_credentials", "client_id": "bkn-agent", "client_secret": "client-secret"}
            return 200, {"access_token": "test-token", "expires_in": 300}
        assert headers["Authorization"] == "Bearer test-token"
        if url.endswith("/internal/trace-evidence/policy"):
            return 200, signed_policy(private_key)
        if url.endswith("/trace-evidence-configuration"):
            return 200, {"kind": "configuration_get", "policy_revision": 11, "active_operation_id": "op-11"}
        return 204, {}
    client = TraceAdmissionClient(
        "http://ao/internal/trace-evidence/policy",
        "http://ao/trace-evidence-configuration",
        "http://ao/internal/trace-evidence/endpoints:heartbeat",
        "http://ao/internal/trace-evidence/operations",
        "http://safe/oauth2/token", "bkn-agent", "client-secret",
        "cluster-a", "test-key", private_key.public_key().public_bytes_raw(),
        request=request,
    )
    assert (await client.read_policy()).revision == 11
    await client.heartbeat("bkn-agent", "boot-1", 11)
    assert await client.operation_for_revision(11) == "op-11"
    summary = DrainSummary("bkn-agent#boot-1", 11, 2, 2, 0, True, "2026-09-27T00:00:00.000Z")
    await client.acknowledge("op-11", summary)
    heartbeat = next(body for method, url, _, body in requests if url.endswith("endpoints:heartbeat"))
    assert heartbeat == {"instance_id": "bkn-agent#boot-1", "process_boot_id": "boot-1", "observed_revision": 11, "ready": True}
    ack = next(body for method, url, _, body in requests if url.endswith("op-11:publisher-ack"))
    assert ack["capture_policy_revision"] == 11
    assert ack["last_accepted_sequence"] == ack["published"] + ack["dropped"]


@pytest.mark.anyio
async def test_configuration_revision_lag_has_no_ack_candidate():
    private_key = Ed25519PrivateKey.generate()
    async def request(method, url, headers, body):
        if url.endswith("/oauth2/token"):
            return 200, {"access_token": "test-token", "expires_in": 300}
        if url.endswith("/trace-evidence-configuration"):
            return 200, {"kind": "configuration_get", "policy_revision": 10, "active_operation_id": "old-op"}
        return 200, signed_policy(private_key)
    client = TraceAdmissionClient(
        "http://ao/internal/trace-evidence/policy", "http://ao/trace-evidence-configuration",
        "http://ao/internal/trace-evidence/endpoints:heartbeat", "http://ao/internal/trace-evidence/operations",
        "http://safe/oauth2/token", "bkn-agent", "client-secret",
        "cluster-a", "test-key", private_key.public_key().public_bytes_raw(), request=request,
    )
    assert await client.operation_for_revision(11) is None


@pytest.mark.anyio
async def test_oauth_transport_sends_form_encoded_client_credentials():
    observed = []
    async def token(request):
        observed.append(dict(await request.post()))
        return web.json_response({"access_token": "test-token", "expires_in": 300})
    app = web.Application()
    app.router.add_post("/oauth2/token", token)
    runner = web.AppRunner(app)
    await runner.setup()
    site = web.TCPSite(runner, "127.0.0.1", 0)
    await site.start()
    port = site._server.sockets[0].getsockname()[1]
    try:
        status, body = await TraceAdmissionClient._http_request(
            "POST", f"http://127.0.0.1:{port}/oauth2/token",
            {"Content-Type": "application/x-www-form-urlencoded"},
            {"grant_type": "client_credentials", "client_id": "bkn-agent", "client_secret": "client-secret"},
        )
        assert status == 200 and body["access_token"] == "test-token"
        assert observed == [{"grant_type": "client_credentials", "client_id": "bkn-agent", "client_secret": "client-secret"}]
    finally:
        await runner.cleanup()
