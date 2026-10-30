# Retained 0.2.0 Evidence enablement and explanation acceptance

This procedure addresses [#2573](https://github.com/openbkn-ai/bkn-foundry/issues/2573).
It keeps the retained Core version and its matching EE assembly. It does not
require a platform upgrade, a plugin rebuild, or a new explanation implementation.
Keep Community/EE route availability separate from Evidence delivery.

## Classify the deployment before changing it

The standalone agent-observability Chart intentionally defaults
`kafkaConsumers.evidence.enabled` to `false`. Audit and Evidence have independent
consumers; working Audit ingestion does not establish Evidence ingestion. The
official complete installer enables Evidence and checks the topic before Core
rollout. A retained service with the consumer disabled therefore needs its
effective deployment values and installer history checked; the standalone
default alone does not establish an installer defect.

The verified startup error `message.timestamp.type is not LogAppendTime` means
the effective topic setting failed the startup contract. It does not establish
the exact configured value or its source. An administrative metadata query that
times out leaves those facts unknown. The guard must remain enabled.

Use the matching release's [official installer procedure](https://github.com/openbkn-ai/openbkn-deploy/blob/main/README.md#internal-trace-admission-protocol-upgrade)
and retain the exact Core/EE/Chart/installer revisions in the deployment record.
Do not use `--latest` as a repair for a retained-version investigation. Export the
existing Helm user/effective values and image digests privately before changes;
effective values can contain secrets. Persist the reviewed configuration in the
values/configuration source used by subsequent installs, rather than patching
the Deployment environment directly.

## Required configuration and admission contract

| Setting or dependency | Required condition |
| --- | --- |
| `core.store`, `core.autoMigrate`, `core.mariadb` | Persistent MariaDB, `autoMigrate=true`, and the existing database Secret. Confirm the matching release's migration ledger first. |
| `evidence.store`, `core.projection.enabled` | Durable artifact storage and the complete install's Core projection profile. Reuse existing storage/authentication settings. |
| `kafkaConsumers.evidence.enabled` | Explicitly `true` in the reviewed deployment values. |
| Evidence topic and group | `openbkn.evidence.v1` and `bkn-trace-evidence-ledger-v1`; preserve the existing group's offsets. |
| Brokers, SASL and Secret | Existing authorized broker configuration and Secret/key references. No new account, password, ACL or inline credential is required by this procedure. |
| Topic timestamp contract | Readable effective `message.timestamp.type=LogAppendTime`, plus a separate disposition for retained records as described below. |
| Live admission | Persisted enabled capture-policy revision, registered producer instance, matching process/stream identity, valid sequence and closure watermark. A topic check alone does not satisfy admission. |
| Producers and artifacts | Matching publisher/control protocol, reachable policy/configuration/heartbeat/ACK endpoints and authenticated artifact delivery. |

An existing persisted capture-policy singleton remains authoritative. On upgrade,
`core.capturePolicyInitialState` only bootstraps an absent singleton. If the old
effective policy state cannot be established, preserve the safe disabled state
until an authorized operator explicitly enables capture through the existing
control surface. Do not turn capture on by guessing the old state.

The installer creates an absent topic with the required timestamp setting; it
does not alter an incompatible existing topic. Confirm this behavior in the
installer version used for the retained release before applying any rollout.

## Preserve and classify retained topic records

Before changing a topic or enabling a previously disabled consumer, retain a
verified Kafka backup, the related MariaDB Ledger/admission state and the
deployment configuration. The [official backup procedure](https://github.com/openbkn-ai/openbkn-deploy/blob/main/BACKUP.zh.md#kafka-在线备份及向已部署环境恢复)
preserves message key/value/headers, source partitions/offsets and consumer
positions. Use a bounded, consistent maintenance window when a cross-component
snapshot is required, including independent/external writers in that plan.

Record the partition ranges and per-record timestamp-type evidence for the
retained data. A current topic setting does **not** prove the type of older
records. Changing the setting does not rewrite them, and republishing/restoring
a record does not turn its old producer timestamp into original broker append
time. A partition's filesystem size is not a consumer-lag measurement.

If retained records are CreateTime or their provenance cannot be established,
stop automatic enablement and obtain a reviewed preservation/migration plan.
Keep the originals and their coordinates; use the existing historical migration
manifest/admission mechanism only for supported, verifiable source records.
Unknown/unreconstructable records remain explicit coverage gaps. This guide does
not authorize topic deletion/recreation, offset skipping/reset, retention
changes, validator bypass or rewriting old records as live capture. Neither a
successful topic check nor consumer startup certifies old data as accepted.

## Verify one real read-only Interaction

After the reviewed deployment change, check the configured Evidence consumer
specifically. `/health/ready` returning HTTP 200 is insufficient: lifecycle
availability is deliberately independent of consumer availability. Inspect the
`consumers.evidence` state and actual durable delivery/rejection evidence. An
absent consumer entry does not demonstrate a running consumer.

Use one authorized, small, real read-only question with an attributable factual
answer. Preserve the first attempt's outcome. Independently read back the same
Interaction, its Operations and matching Receipts, then verify:

1. The recorded `agent.interaction.started` event references a question artifact
   and its content hash. That artifact resolves within the same Interaction and
   its captured content passes hash verification. Detail-page text alone does
   not establish this relationship.
2. Required result-completeness events and artifacts are present; matching
   Receipts reach durable Evidence state. Business execution `completed` and
   pending/partial Evidence are separate facts.
3. Native EE detail shows the actual question/answer and Operations. Once the
   recorded capture is available, explicitly POST explanation generation once;
   verify nonempty factual claims and genuine claim-to-evidence attribution.
   An evidence graph with nodes/edges alone is not attribution.
4. Interpret outer explanation `status=ready` as a stored snapshot, and inspect
   its `generationStatus`, claims and notices separately. `analysis_pending` is
   not successful attribution. GET reads the stored snapshot; it does not
   regenerate it after later Core state changes. Preserve an earlier pending
   snapshot, and explicitly regenerate the same Interaction after remediation
   instead of rerunning the business question to conceal the failure.

Record revisions, timestamps, identities, durable acknowledgements, rejection
reasons and the before/after explanation states privately. Do not attach business
bodies, credentials or private deployment details to a public issue. Until this
verification succeeds, keep #2573's environment remediation open; documentation
or a green unit test cannot close that acceptance requirement.

## Rollback

Restore the prior reviewed Helm revision/configuration and matching service
assembly if rollout fails. Preserve database rows, Kafka messages, offsets and
the original failure evidence. Any topic/data migration has its own reviewed
rollback plan; Helm rollback does not undo it.
