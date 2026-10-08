# Internal Trace/Evidence control API

The internal listener is `agent-observability-internal:8081`. All paths below start with `/api/agent-observability/v1/internal/trace-evidence`. These routes require no OAuth token, Access Profile, policy signature, key ID, or audience. They are mounted only on the internal listener; the public listener returns 404 for these paths. Public user configuration routes retain their existing authentication.

## Read policy

`GET /policy` returns 200 with an unsigned snapshot:

```json
{"contract_version":"TraceEvidencePolicySnapshotV1","revision":42,"trace_admission":"enabled","evidence_admission":"enabled","issued_at":"2026-10-08T02:00:00Z","expires_at":"2026-10-08T02:15:00Z"}
```

Both admission fields have the same value (`enabled` or `disabled`). Revision is positive. Consumers reject malformed, future-issued, expired and stale-revision snapshots. A refresh at the same revision may renew expiry but cannot change admission state. TTL defaults to 15 minutes, configured with `core.capturePolicySnapshotTTL` / `BKN_TRACE_CAPTURE_POLICY_SNAPSHOT_TTL`.

## Read configuration

`GET /configuration` returns the existing `ConfigurationGetResponse`, including current revision, desired/effective state, active operation and admission budget. Clients use the operation ID and revision when sending acknowledgements. This route accepts GET only.

## Endpoint heartbeat

`POST /endpoints:heartbeat` accepts:

```json
{"endpoint_kind":"evidence_publisher","workload_identity":"bkn-agent","instance_id":"bkn-agent#boot-1","process_boot_id":"boot-1","observed_revision":42,"ready":true}
```

`endpoint_kind` is `trace_gateway` or `evidence_publisher`. Workload identity and process boot ID are caller-supplied state identifiers. `instance_id` must equal `workload_identity#process_boot_id`. Identity fields and a positive revision are required; unknown fields are rejected. The server records a 30-second lease. Ready evidence publishers register their heartbeat atomically with expected acknowledgement membership.

## Gateway acknowledgement

`POST /operations/{id}:ack` retains `TraceGatewayAcknowledgementV1`: `contract_version`, `gateway_instance_id`, `workload_identity`, `process_boot_id`, `capture_policy_revision`, `admission_state`, `ready`, `acknowledged_at`, and `queue_disposition`. Identity comes from this body; the gateway instance must equal `workload_identity#process_boot_id`.

Required queue counters, explicit nullable `unaccounted`, allowed gap reasons, enabled/disabled disposition rules, current revision and active operation checks remain unchanged.

## Publisher acknowledgement

`POST /operations/{id}:publisher-ack` retains the publisher closure body:

```json
{"producer_instance_id":"bkn-agent#boot-1","capture_policy_revision":42,"last_accepted_sequence":7,"published":5,"dropped":2,"queue_empty":true,"acknowledged_at":"2026-10-08T02:00:10Z"}
```

The server derives workload identity and boot ID by splitting `producer_instance_id` at the final `#`; both parts must be nonempty. Queue closure requires `last_accepted_sequence = published + dropped` and `queue_empty=true`. Current revision, active operation and expected publisher membership checks remain unchanged.

Successful POST requests return 204. Malformed bodies return 400; stale or unexpected operation state returns 409; unavailable dependencies return 503. GET endpoints support GET only and POST endpoints support POST only (405 otherwise). These process state fields do not authenticate the caller.
