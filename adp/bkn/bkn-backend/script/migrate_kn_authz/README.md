# Migrate Knowledge-Network Authorization Data

This standalone script rebuilds the seven knowledge-network authorization
resource types in bkn-safe from the BKN business database.

The script intentionally removes existing KN instance, role, and public policies.
After migration, the authorization baseline contains only:

- one `knowledge_network:*:create` policy for `network_builder`;
- the fixed creator policies for each knowledge network;
- one `execute` creator policy for each action type;
- one parent edge from every child resource to its knowledge network.

Run it only while BKN, ontology-query, execution-factory, and bkn-safe are stopped,
and only after both databases have been backed up.

## Requirements

- Python 3.9+
- PyMySQL 1.1.0
- MariaDB or MySQL access to the BKN and bkn-safe databases

```bash
python3 -m pip install pymysql==1.1.0
```

## Configuration

The default database names are `openbkn` and `safe`. Configure connections with
the following environment variables:

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

Connection values other than passwords can also be supplied as command-line
arguments. Run `python3 script.py --help` for the complete list.

## Usage

Always start with a dry run:

```bash
python3 script.py --dry-run --report kn-authz-dry-run.json
```

The dry run reads both databases, validates IDs, branch uniqueness, parent
existence, and creator accounts, then prints the planned row counts. It performs
no writes. A validation failure returns a non-zero exit code.

After reviewing the report and confirming the backups, apply the same plan:

```bash
python3 script.py --apply --report kn-authz-apply.json
```

Safe policy deletion and reconstruction run in one database transaction. Blank
BKN branches are normalized to `main` in a separate BKN transaction. The script
is idempotent and can be run again after a failure.

## Focused test

```bash
python3 -m unittest -v test_script.py
```
