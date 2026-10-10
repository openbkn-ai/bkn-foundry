# System Audit deployment profile

## Complete platform installation and upgrades

System Audit is a required platform capability, independent of Trace/Evidence
capture configuration. The complete `deploy.sh openbkn install` path prepares
Audit consumer/publisher defaults and validates both before rollout. It repairs
historical disabled deployment settings; current explicit disable settings fail
preflight rather than report a complete installation. The standalone Chart and
the manual profile below retain their development defaults.

The installer reuses the Kafka client Secret already prepared in the platform
namespace; it creates no Kafka account, rotates no credentials and changes no
ACLs. Saved connection configuration is preserved below current config/CLI.
An explicit registered `auditPublisher.environment` is required on first install
when there is no saved value; the installer does not guess `production`. For
production, supply an existing independently approved publisher Secret reference
with the required write authorization. A different Secret name alone is not
proof of a distinct principal or correct permissions; deployment owners must
confirm the existing identity/ACL policy and real-event acceptance below.

For example, using actual owner-approved values (replace both placeholders):

```bash
bash deploy/deploy.sh openbkn install \
  --set auditPublisher.environment=<registered-environment> \
  --set auditPublisher.existingSecret.name=<approved-existing-publisher-secret>
```

These non-sensitive settings become durable Helm values. Later upgrades do not
require repeating them. An equal Chart version still renders the final values
and checks both components; identical reconciled settings skip rollout, changed
settings do not. Secret lookup and Kafka topic failures stop installation.

Topic preparation uses the existing bundled Kafka broker tools with the Audit
consumer credentials, supplied through stdin and a private temporary properties
file. Only a missing `openbkn.audit.v1` is created (one partition, one replica,
`LogAppendTime`); an existing topic's settings/partitions/retention/offsets are
not modified. An existing `CreateTime` topic or inaccessible topic configuration
fails preflight. This preparation path is scoped to the bundled Kafka deployment;
external-only deployments without its tooling receive an explicit diagnostic.

Topic preflight proves authentication and visible timestamp configuration, not
publisher write authorization or consumer group consumption. Real event
acceptance remains necessary. Close and restore Trace/Evidence capture through
the normal authorized API in a disposable test environment, and verify both
requested events and their legitimate terminal events remain publicly queryable.
Audit must continue while capture is disabled. Empty HTTP 200 results are not
acceptance evidence. Keep the existing no-op/GET/401/403/validation-rejection
semantics; do not create synthetic audit events to replace real action testing.

## Separately managed Chart releases

`system-audit.yaml` is an explicit opt-in profile for deployments requiring
`audit.admin`. It is never loaded automatically. The bare Chart remains disabled.
Copy it into the deployment's reviewed values and fill the broker addresses and
existing Kafka/MariaDB Secret references; the empty placeholders intentionally
fail Helm validation. Keep the resulting values as the durable upgrade input.

The Kafka consumer principal and the producer principals remain independently
managed. This profile creates no users, Secrets, ACLs, roles or permissions.
Do not enable the optional control Audit publisher with consumer credentials;
configure that producer separately with its existing write principal if those
management events are required.

Before rollout, verify the existing `openbkn.audit.v1` topic is reachable with the
consumer's current credentials and its effective `message.timestamp.type` is
`LogAppendTime`. The existing consumer startup performs this same check and
refuses to start when it cannot prove the setting; an empty query is not proof
that records have arrived. Infrastructure operators can use their existing
Kafka tooling to read the topic configuration, for example:

```bash
kafka-configs.sh --bootstrap-server "$BROKER" --command-config "$CLIENT_CONFIG" \
  --entity-type topics --entity-name openbkn.audit.v1 --describe
```

For the Foundry installer, merge the profile's `core` and `kafkaConsumers.audit`
settings into the reviewed platform configuration, retaining other settings:

```bash
bash deploy/deploy.sh openbkn install --config "$PLATFORM_VALUES" --force-upgrade
```

The existing installer validates enabled consumer values and nonempty Secret
keys before upgrading. It preserves the installed Audit consumer values on a
later upgrade, with the current values/CLI taking precedence. Its Secret
preflight does not prove Kafka connectivity; the topic check and deployment
startup remain required. `core.autoMigrate=true` requires the existing database
migration privileges for the fixed Core and Audit schemas; have the database
owner verify them rather than widening runtime grants automatically.

For a separately managed Chart release, pass the reviewed profile on every
upgrade together with the currently installed configuration. Capture only your
own release's values in a private file so other durable settings are retained:

```bash
umask 077
CURRENT_VALUES="$(mktemp)"
helm get values agent-observability -n "$NAMESPACE" -o yaml >"$CURRENT_VALUES"
helm upgrade agent-observability openbkn/agent-observability \
  --version "$CHART_VERSION" -n "$NAMESPACE" \
  -f "$CURRENT_VALUES" -f "$AUDIT_VALUES" --wait
rm -f "$CURRENT_VALUES"
```

Never use temporary Pod environment patches as the deployment record. Explicit
`kafkaConsumers.audit.enabled: false` disables the consumer without reading its
credential Secret.

After rollout, use an existing authorized administrator to perform a reviewed
management action through its normal UI/API. Record its actual event ID from the
source/ledger and run the read-only verifier using a short window around that
action and an existing authorized query token supplied through the environment:

```bash
python3 deploy/scripts/audit/verify_system_audit.py \
  --base-url "$PLATFORM_URL" --event-id "$EVENT_ID" --source-id "$SOURCE_ID" \
  --time-from "$TIME_FROM" --time-to "$TIME_TO"
```

The verifier reads `BKN_AUDIT_VERIFY_TOKEN`; it never produces Audit records,
grants roles, logs credentials or follows redirects. A healthy source plus the
specified `audit.admin` event must both be visible. An empty list fails this
collection check. Use a narrow window containing at most 100 relevant events;
an event outside that first page fails the check rather than being guessed.
A normal user must still receive 403 for an unauthorized global audit query.

Control configuration events preserve the current actor name from the already
verified Safe `/me` response (display name, or the trusted login account when
name is blank, matching Safe audit behavior) and the server-owned configuration/operation name
at production time. Missing actor names remain an explicit publisher coverage
gap; management requests remain fail-open for Audit delivery. No ID fallback or
query-time name repair is used. Existing rows without snapshots remain unchanged.
