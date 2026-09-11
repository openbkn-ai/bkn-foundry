# OpenBKN 0.1.5 Permission-Model Transition

[中文](README.zh.md) | English

This directory is the only operator-facing entry for the one-time OpenBKN
0.1.4 to 0.1.5 permission-model transition. It deliberately does not use the
general `data-migrator`: this workflow needs a maintenance window, a reviewed
dry-run, explicit Enterprise activation evidence, and a coordinated backup of
multiple product databases.

Fresh installations receive the current authorization marker from bkn-safe
seed data. Later releases that keep the same authorization storage contract do
not run this workflow again.

## Registered steps

The entry runs a fixed, fail-fast sequence:

1. `bkn-data` validates or applies blank-branch normalization, authoritative
   BKN resource-parent rows, managed proxy accounts, Resource/Tool Box/MCP
   grant sources, materialized proxy policies, and BKN proxy mappings.
2. `authorization` invokes this directory's `authz_migrate` executable to
   classify Core provenance and stable grants, reconcile Enterprise rules,
   apply explicitly confirmed activation, and persist the checksummed marker.

The BKN step never deletes or rebuilds caller authorization policies. In
particular, it never writes `task_manage`. Historical Core allow/deny rows are
classified by the authorization step instead of being replaced.

A future Vega migration for this same target release must be added as another
explicit step in `migrate.py`; it must not create a second operator command.

## Requirements

- Python 3.9+ and PyMySQL 1.1.0;
- `mariadb-dump` or `mysqldump` and enough space for complete BKN and Safe
  logical backups;
- `kubectl` access to the target cluster;
- Go 1.25+ to build the release-owned authorization migration executable, or
  the prebuilt executable supplied with the release artifact;
- MariaDB/MySQL access to the BKN and Safe databases.

The BKN step resolves `BKN_DB_*` and `SAFE_DB_*` variables first, then standard
`MARIADB_*` variables, and finally local defaults. Password files are supported
through `BKN_DB_PASSWORD_FILE`, `SAFE_DB_PASSWORD_FILE`, and the corresponding
MariaDB password-file variables. Set `OPENBKN_MIGRATION_BACKUP_DIR` when the
directory beside this script is not an appropriate backup volume.

## Workflow

Copy `manifest.example.json` to a protected working location and replace every
placeholder with authoritative release, lifecycle, and Enterprise evidence.

Build the release-owned authorization step when the release artifact does not
already contain it:

```bash
./authz_migrate/build.sh
```

Run both read-only plans and preserve their reports:

```bash
./migrate.py dry-run \
  --source-version 0.1.4 \
  --manifest /work/authz-migration.json \
  --authz-config /etc/bkn-safe/config.yaml \
  --report-dir /work/reports/dry-run
```

Review `01-bkn-data.json`, `02-authorization.json`, and `summary.json`. Add the
exact Enterprise inventory digest and activation confirmation to the manifest
when historical EE rules are intentionally activated. No historical operation
set may be inferred as `full_business_access`, and no operation may be added.

Close external gateways and disable relevant CronJobs/workers, then stop the
registered application Deployments:

```bash
./migrate.py stop \
  --namespace openbkn \
  --state-file /work/workloads.tsv
```

Apply with a new, empty report directory:

```bash
./migrate.py apply \
  --source-version 0.1.4 \
  --manifest /work/authz-migration.json \
  --authz-config /etc/bkn-safe/config.yaml \
  --state-file /work/workloads.tsv \
  --namespace openbkn \
  --report-dir /work/reports/apply
```

`apply` verifies through the live cluster that every registered Deployment is
still stopped. The BKN step creates and verifies complete logical backups of
both databases before its first write. It then commits BKN/proxy data before
the authorization step runs; if either step fails, the remaining steps do not
run and workloads stay stopped.

After reviewing the apply reports, start the new binaries while the external
entry remains closed, complete the documented authorization smoke tests, and
only then reopen traffic:

```bash
./migrate.py start \
  --namespace openbkn \
  --state-file /work/workloads.tsv
```

## Failure recovery

Do not start workloads after a failed step. Restore both logical backups named
in `01-bkn-data.json`, deploy all previous binaries, and verify the previous
authorization behavior before reopening traffic. Never create a success marker
manually or continue on a partially migrated database.

## Focused tests

```bash
python3 -m unittest -v test_bkn_data.py test_migrate.py
./test_service_control.sh
(cd authz_migrate && go test -p=1 ./...)
```
