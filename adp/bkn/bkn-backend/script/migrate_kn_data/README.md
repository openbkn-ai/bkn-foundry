<!--
Copyright openbkn.ai

Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
-->

# Migrate Knowledge-Network Data

This is the only user-facing data migration command for an OpenBKN 0.1.4 to
0.1.5 upgrade. System installation and upgrade remain owned by
`deploy/deploy.sh openbkn install`; run this script separately only when an
existing installation has 0.1.4 knowledge-network data.

The installation or upgrade command must finish first. This data script never
creates or changes the 0.1.5 database schema; it fails validation when the
required BKN or bkn-safe tables are unavailable.

One execution performs all required data work:

- normalizes blank BKN branches to `main`;
- rebuilds the seven knowledge-network authorization resource types in
  bkn-safe;
- creates one credential-free managed app account for every existing main
  knowledge network;
- derives Resource, Tool Box, and MCP grants from the persisted main model;
- materializes the proxy source ledger, Casbin policies, and BKN proxy mapping;
- records identical published and synchronized model versions with `ready`
  status; and
- verifies the complete result before committing both database connections.

The caller-authorization part intentionally replaces existing KN instance,
role, and public policies. After migration, that authorization baseline contains:

- one `knowledge_network:*:create` policy for `network_builder`;
- the fixed creator policies for each knowledge network;
- one `execute` creator policy for each action type;
- one parent edge from every child resource to its knowledge network.

The command only reads and writes database data. It does not install or upgrade
OpenBKN, control workloads, create a backup, write a report file, or call a
running service or HTTP API.

## Requirements

- Python 3.9+
- PyMySQL 1.1.0
- MariaDB or MySQL access to the BKN and bkn-safe databases

```bash
python3 -m pip install pymysql==1.1.0
```

## Configuration

The default database names are `openbkn` and `safe`. The command reads database
connections from the existing runtime environment:

```bash
export BKN_DB_HOST=localhost
export BKN_DB_PORT=3306
export BKN_DB_USER=root
export BKN_DB_PASSWORD='<password>'
export BKN_DB_NAME=openbkn

export SAFE_DB_HOST=localhost
export SAFE_DB_PORT=3306
export SAFE_DB_USER=root
export SAFE_DB_PASSWORD='<password>'
export SAFE_DB_NAME=safe
```

The two databases normally use the same MariaDB instance and credentials. When
`SAFE_DB_HOST`, `SAFE_DB_PORT`, `SAFE_DB_USER`, or `SAFE_DB_PASSWORD` is not
set, the command automatically reuses its corresponding `BKN_DB_*` value. Only
`SAFE_DB_NAME` defaults independently to `safe`.

## Usage

After the 0.1.5 installation or upgrade has completed, run these three commands:

```bash
./service_control.sh stop
./script.py
./service_control.sh start
```

`service_control.sh` automatically reads the current kubectl context. The stop
command records that context and the four original replica counts in its
internal state file. The start command refuses to restore workloads if the
current context no longer matches the recorded context.

The migration command validates all source data, applies the caller-authorization and
managed-proxy migrations, verifies the result, and then exits. It uses the
fixed bkn-safe built-in `admin` identity as the migration grantor; users do not
need to find or pass an account ID.

The migration is idempotent: it reuses existing managed accounts and mappings,
reactivates matching source rows, and reconciles owned policies. A successful
execution returns zero and prints a completion message. A failure returns non-zero
and prints the complete Python traceback plus every validation failure to
standard error.

If migration fails, leave the services stopped, correct the reported problem,
and rerun `./script.py`. Run `./service_control.sh start` only after migration
completes successfully. The service-control script does not stop MariaDB or
other infrastructure workloads.

## Focused test

```bash
python3 -m unittest -v test_script.py
./test_service_control.sh
```
