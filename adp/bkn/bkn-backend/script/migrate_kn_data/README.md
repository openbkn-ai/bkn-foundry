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

- creates and verifies logical backups of the BKN and bkn-safe databases before
  the first migration write;
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

The command reads and writes database data and creates a local backup manifest.
It does not install or upgrade OpenBKN, control workloads, or call a running
service or HTTP API.

## Requirements

- Python 3.9+
- PyMySQL 1.1.0
- `mariadb-dump` or `mysqldump` in `PATH`
- MariaDB or MySQL access to the BKN and bkn-safe databases
- enough writable disk space for full compressed logical backups of both
  databases

```bash
python3 -m pip install pymysql==1.1.0
```

## Configuration

Run the script on the database server or inside its database container. It
automatically reads standard MariaDB container variables and defaults to a
local database server at `127.0.0.1:3306`.

Configuration is resolved in this order:

1. explicit `BKN_DB_*` or `SAFE_DB_*` variables;
2. `MARIADB_*` variables provided by the database server/container;
3. local defaults (`127.0.0.1:3306`, user `root`, databases `openbkn` and
   `safe`).

Direct password values and password files are both supported. The relevant
standard variables are `MARIADB_PASSWORD`, `MARIADB_PASSWORD_FILE`,
`MARIADB_ROOT_PASSWORD`, and `MARIADB_ROOT_PASSWORD_FILE`. Explicit overrides
can use `BKN_DB_PASSWORD_FILE` and `SAFE_DB_PASSWORD_FILE` as well as the direct
password variables shown below.

The explicit overrides are:

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
set, the command automatically reuses the resolved BKN connection. Only
`SAFE_DB_NAME` defaults independently to `safe`.

The script does not query Kubernetes Secrets. If it is run outside the database
server/container, provide the explicit overrides above. An unreadable password
file or invalid port stops the command before any database connection or write.

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

## Backup and restore

After validation and immediately before the first write, the script creates:

```text
backups/YYYYMMDD_HHMMSS/
├── bkn.sql.gz
├── safe.sql.gz
└── manifest.json
```

`backups` is located beside `script.py`. Set
`OPENBKN_MIGRATION_BACKUP_DIR` only when that directory is not writable or a
different backup volume is required. Existing backup directories and files are
never overwritten; a same-second rerun receives a numeric suffix.

Each archive is a full logical dump created with transaction-consistent dump
options and includes routines, events, triggers, and binary data. The manifest
records file sizes, SHA-256 checksums, and password-free restore commands. The
resolved database password is passed to the dump process through its environment;
it is never included in command arguments, terminal output, or the manifest.

The script prints the backup directory and restore commands before migration
writes begin. To restore, keep the services stopped, set `MYSQL_PWD` from the
same environment or password file, and run the applicable printed command. For
example:

```bash
export MYSQL_PWD="$(cat /path/to/database-password-file)"
gzip -dc backups/YYYYMMDD_HHMMSS/bkn.sql.gz \
  | mariadb --host=127.0.0.1 --port=3306 --user=root
```

Restore both archives before retrying when a failed run may have committed one
database but not the other. A missing dump utility, unwritable backup directory,
failed dump, empty archive, or checksum-verification failure stops execution
before the first migration write. Previously completed backups remain untouched.

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
