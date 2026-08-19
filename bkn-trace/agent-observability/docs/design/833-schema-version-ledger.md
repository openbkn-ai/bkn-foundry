# BKN Trace Schema Version Ledger

## Status

Proposed for issue #833.

## Goal

Make a MariaDB-backed Trace Core reject schema/image incompatibility during
startup instead of allowing the first managed operation to fail at runtime.

## Decision

The Trace service remains the owner of its embedded MariaDB schema.  It will
use an append-only ordered migration manifest rather than concatenating all SQL
on every startup.  Each entry has a stable version and SHA-256 checksum.

`bkn_trace_schema_migrations` records each applied version, checksum and
application time.  Before changing schema the service takes a MariaDB advisory
lock.  It then verifies every recorded checksum, rejects a database newer than
the running binary, and applies only missing migrations when auto-migration is
enabled.  When migration is disabled, a database behind the binary is a
startup error that states both versions and the enabling configuration.

The v013 baseline is represented as the first ledger version.  It is not a
data conversion from legacy 0.1.3 deployments: unsupported historic Trace
data remains outside this contract and is handled by the existing clean-slate
operator procedure.

## Failure semantics

- Invalid `BKN_TRACE_CORE_AUTO_MIGRATE` is a configuration error; it is never
  silently treated as `false`.
- The setting defaults to `true` for the MariaDB Core path, matching the Helm
  default.  Memory store behaviour is unchanged.
- A missing ledger on a non-empty pre-ledger database is refused rather than
  guessed.  A clean database is initialized by the versioned manifest.
- Checksum mismatch, database-ahead, lock failure, or DDL failure fail app
  construction before readiness and workers start.
- The advisory lock is released on every return path.

## Verification

Unit tests cover manifest ordering/checksums and configuration parsing.  A
MariaDB integration test (enabled only with `BKN_TRACE_TEST_MARIADB_DSN`) runs
migration twice and checks ledger state, covering idempotency against a real
server.

## Operations

The repository migration configuration documents that `bkn_trace` is
self-managed, including why it does not participate in data-migrator rules.
The deployment guide explains the fail-fast messages and the clean-slate path
for unsupported 0.1.3 data.
