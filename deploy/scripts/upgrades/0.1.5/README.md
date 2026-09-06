<!--
Copyright openbkn.ai

Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
-->

# OpenBKN 0.1.4 to 0.1.5 Knowledge-Network Migration

`migrate.py` is the only user-facing entry for this upgrade. It keeps schema,
caller authorization, managed-proxy backfill, verification, and rollback in one
JSON report/checkpoint. Re-running a phase skips completed network checkpoints.
An already completed phase is skipped unless `--force` is supplied.

The tool never enables managed-proxy traffic. Keep `knProxy.mode=off` until the
`verify` phase writes `safe_to_enable_proxy: true`; then use `allowlist` for a
canary before changing the mode to `all`.

## Safety model

- `schema --apply` and `caller-authz --apply` require stopped BKN,
  ontology-query, execution-factory, and bkn-safe workloads plus a non-empty
  database-backup artifact. Caller migration also requires a non-empty export
  of the pre-migration Policy and ResourceParent rows.
- Online phases require the operator to attest `--runtime-ready` and pass
  `--proxy-mode off`.
- Proxy grants are materialized only through BKN Backend and bkn-safe source
  APIs. The versioned tool never writes proxy Casbin Policy rows directly.
- Rollback does not restore revoked caller permissions. It disables, clears
  grant sources from, and archives only proxies recorded as created by this
  migration. The schema table is retained for older/newer binary compatibility.
- An existing proxy without a matching checkpoint is reported as a conflict
  and blocks backfill; the tool will not mutate state it cannot safely roll back.

Set database passwords through `BKN_DB_PASSWORD` and `SAFE_DB_PASSWORD`; avoid
placing credentials in shell history. The examples omit repeated connection
arguments for brevity.

## Procedure

Create an empty checkpoint path and inspect the schema change during the outage:

```bash
python3 deploy/scripts/upgrades/0.1.5/migrate.py schema \
  --state /secure/upgrade-0.1.5.json \
  --dry-run
```

After stopping the four workloads and creating the required backup/export:

```bash
python3 deploy/scripts/upgrades/0.1.5/migrate.py schema \
  --state /secure/upgrade-0.1.5.json \
  --apply --workloads-stopped \
  --database-backup /secure/openbkn-and-safe.sql

python3 deploy/scripts/upgrades/0.1.5/migrate.py caller-authz \
  --state /secure/upgrade-0.1.5.json \
  --apply --workloads-stopped \
  --database-backup /secure/openbkn-and-safe.sql \
  --caller-export /secure/kn-policy-resource-parent.json
```

Deploy and start the 0.1.5 workloads with `knProxy.mode=off`. Plan the API phase,
then apply the serial proxy backfill. Both commands use the same checkpoint:

```bash
python3 deploy/scripts/upgrades/0.1.5/migrate.py plan \
  --state /secure/upgrade-0.1.5.json \
  --backend-url http://bkn-backend-svc:13014 \
  --user-id <authorized-human-id> \
  --runtime-ready --proxy-mode off

python3 deploy/scripts/upgrades/0.1.5/migrate.py proxy-backfill \
  --state /secure/upgrade-0.1.5.json \
  --backend-url http://bkn-backend-svc:13014 \
  --user-id <authorized-human-id> \
  --runtime-ready --proxy-mode off
```

Verify mappings, source plans, synchronized model versions, reconciliation,
managed-account login/credential restrictions, and both downstream PEPs:

```bash
python3 deploy/scripts/upgrades/0.1.5/migrate.py verify \
  --state /secure/upgrade-0.1.5.json \
  --backend-url http://bkn-backend-svc:13014 \
  --safe-url http://bkn-safe:3000 \
  --user-id <authorized-human-id> \
  --runtime-ready --proxy-mode off \
  --pep-ready vega --pep-ready execution-factory
```

The `--pep-ready` values are explicit deployment attestations backed by the
release's downstream PEP tests. They are required because no unauthenticated
runtime probe can safely attempt a third-party Tool or MCP side effect.

To roll back before or after a canary, first set `knProxy.mode=off`, stop new
proxy traffic, and run:

```bash
python3 deploy/scripts/upgrades/0.1.5/migrate.py rollback \
  --state /secure/upgrade-0.1.5.json \
  --backend-url http://bkn-backend-svc:13014 \
  --safe-url http://bkn-safe:3000 \
  --user-id <authorized-human-id> \
  --runtime-ready --proxy-mode off
```

## Network boundary

Standard Kubernetes NetworkPolicy is L3/L4 and cannot distinguish public from
internal HTTP paths on the same workload port. Enable each chart's
`knProxy.networkPolicy.enabled` only after reviewing its explicit caller list.
The complete boundary is the combination of that source allowlist and Ingress
publishing only `/v1` public paths; `/in/v1` and `/internal-v1` remain absent.
The bkn-safe chart keeps its existing `networkPolicy.enabled` switch and
verbatim `networkPolicy.ingress` rules because its shared port also serves other
authorization clients. Include every legitimate peer, with BKN Backend
explicitly allowed to reach the managed-account and grant-source APIs, before
enabling that policy.

## Focused tests

```bash
python3 deploy/scripts/upgrades/0.1.5/test_migrate.py -v
bash deploy/scripts/upgrades/0.1.5/test_helm_rollout.sh
```
