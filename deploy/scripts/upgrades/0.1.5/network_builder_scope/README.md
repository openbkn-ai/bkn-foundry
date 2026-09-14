# Network-builder object scope backfill

Run this package **before** deploying a bkn-safe image whose `network_builder`
seed grants contain only `catalog:* / create` and `knowledge_network:* / create`.

The script reads `f_creator` from Catalog and Knowledge Network metadata. It
does not derive ownership from old effective permissions. For each non-internal
Catalog and Knowledge Network with no direct creator grant, it plans the two
canonical bkn-safe records: a `community_bundle` business grant and a
`system_derived` `authorize` grant.

It never changes existing complete grants. A missing creator, non-user creator,
or a partial direct grant blocks the run rather than broadening access.

Take a database backup, run dry-run, review the JSON report, then use apply in a
maintenance window:

```bash
python3 backfill_creator_grants.py --mode dry-run --bkn-dsn "$BKN_DSN" --safe-dsn "$SAFE_DSN"
python3 backfill_creator_grants.py --mode apply --confirm-apply backfill-creator-grants --bkn-dsn "$BKN_DSN" --safe-dsn "$SAFE_DSN"
```

`apply` writes `authorization_grant` and its corresponding `casbin_rule`
projection in one bkn-safe database transaction. It must finish successfully
before restarting bkn-safe with the narrowed seed grants.
