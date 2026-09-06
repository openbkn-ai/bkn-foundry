<!--
Copyright openbkn.ai

Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
-->

# Backfill Knowledge-Network Managed Proxies

> Deprecated compatibility entry: this script alone does not complete an
> OpenBKN 0.1.4 to 0.1.5 upgrade. Use the versioned migration entry at
> `deploy/scripts/upgrades/0.1.5/migrate.py`.

This script uses BKN Backend's internal governance endpoints so the dry run and
the applied migration share the runtime's latest-main source derivation. The
caller must be a human grantor with `authorize` and every requested target
operation. Stop concurrent knowledge-network publication before migration.

Always produce and review a dry-run report first:

```bash
python3 script.py \
  --backend-url http://bkn-backend:13011 \
  --user-id <authorized-user-id> \
  --dry-run \
  --report kn-proxy-dry-run.json
```

The report contains every derived resource/Tool Box/MCP source and whether a
mapping already exists. After review and the repository-required deployment
confirmation, repeat with `--apply`; each network is synchronized serially and
the operation stops on the first failure.
