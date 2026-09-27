"""Small split-capability CLI for the one-time Evidence migration bridge."""

import argparse
import json
import os
import re
import sys
from pathlib import Path

from bridge import load_active_artifact, publish_encoded_entries
from manifest import ManifestError
from snapshot import issue_manifest, read_event_snapshot, verify_frozen_entries

MAX_KAFKA_REQUEST_SIZE = 2_097_152
_HASH = re.compile(r"[0-9a-f]{64}\Z")
_SOURCE_TABLES = {"bkn_backend_trace_outbox", "ontology_query_trace_outbox"}


def _load_core_ownership_gaps(path):
    if path is None:
        return None
    try:
        rows = json.loads(Path(path).read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        raise ManifestError("historical core ownership gap list is unreadable") from error
    if not isinstance(rows, list):
        raise ManifestError("historical core ownership gap list must be an array")
    if not rows:
        raise ManifestError("historical core ownership gap list cannot be empty; omit the option when there are no gaps")
    gaps = {}
    required = {"source_table", "source_primary_key", "event_id", "payload_hash"}
    for row in rows:
        if not isinstance(row, dict) or set(row) != required:
            raise ManifestError("historical core ownership gap row shape is invalid")
        table, primary_key = row["source_table"], row["source_primary_key"]
        event_id, payload_hash = row["event_id"], row["payload_hash"]
        if (table not in _SOURCE_TABLES or not isinstance(primary_key, str) or
                not primary_key.isdecimal() or str(int(primary_key)) != primary_key or int(primary_key) <= 0 or
                not isinstance(event_id, str) or not event_id or
                not isinstance(payload_hash, str) or _HASH.fullmatch(payload_hash) is None):
            raise ManifestError("historical core ownership gap identity is invalid")
        key = (table, primary_key)
        if key in gaps:
            raise ManifestError("historical core ownership gap row is duplicated")
        gaps[key] = (event_id, payload_hash)
    return gaps


def _source_connection():
    try:
        import pymysql
    except ImportError as error:
        raise ManifestError("install the migration requirements before connecting to the source database") from error
    required = ("BKN_EVIDENCE_SOURCE_HOST", "BKN_EVIDENCE_SOURCE_USER", "BKN_EVIDENCE_SOURCE_DATABASE")
    missing = [key for key in required if not os.environ.get(key)]
    if missing:
        raise ManifestError("missing source database configuration: " + ", ".join(missing))
    return pymysql.connect(
        host=os.environ["BKN_EVIDENCE_SOURCE_HOST"],
        port=int(os.environ.get("BKN_EVIDENCE_SOURCE_PORT", "3306")),
        user=os.environ["BKN_EVIDENCE_SOURCE_USER"],
        password=os.environ.get("BKN_EVIDENCE_SOURCE_PASSWORD", ""),
        database=os.environ["BKN_EVIDENCE_SOURCE_DATABASE"],
        charset="utf8mb4",
        autocommit=True,
        connect_timeout=5,
        read_timeout=60,
        write_timeout=5,
    )


def _kafka_producer():
    try:
        from kafka import KafkaProducer
    except ImportError as error:
        raise ManifestError("install the migration requirements before connecting to Kafka") from error
    bootstrap = os.environ.get("BKN_EVIDENCE_KAFKA_BOOTSTRAP_SERVERS", "")
    if not bootstrap:
        raise ManifestError("BKN_EVIDENCE_KAFKA_BOOTSTRAP_SERVERS is required")
    config = {
        "bootstrap_servers": [value.strip() for value in bootstrap.split(",") if value.strip()],
        "acks": "all",
        "enable_idempotence": True,
        "retries": 4,
        "retry_backoff_ms": 100,
        "max_in_flight_requests_per_connection": 1,
        "request_timeout_ms": 5000,
        "delivery_timeout_ms": 30000,
        "max_block_ms": 5000,
        # Event values remain capped at the C1 1 MiB contract. The Kafka
        # request also carries the key, headers and record framing.
        "max_request_size": MAX_KAFKA_REQUEST_SIZE,
    }
    protocol = os.environ.get("BKN_EVIDENCE_KAFKA_SECURITY_PROTOCOL", "PLAINTEXT")
    config["security_protocol"] = protocol
    if protocol.startswith("SASL_"):
        mechanism = os.environ.get("BKN_EVIDENCE_KAFKA_SASL_MECHANISM", "")
        username = os.environ.get("BKN_EVIDENCE_KAFKA_SASL_USERNAME", "")
        password = os.environ.get("BKN_EVIDENCE_KAFKA_SASL_PASSWORD", "")
        if not all((mechanism, username, password)):
            raise ManifestError("SASL Kafka configuration is incomplete")
        config.update(sasl_mechanism=mechanism, sasl_plain_username=username, sasl_plain_password=password)
    return KafkaProducer(**config)


def _begin_readonly_snapshot(connection):
    cursor = connection.cursor()
    try:
        cursor.execute("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
        cursor.execute("START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY")
        cursor.execute("SELECT DATE_FORMAT(UTC_TIMESTAMP(3),'%Y-%m-%dT%H:%i:%s.%fZ')")
        value = cursor.fetchone()[0]
        return value[:-4] + "Z" if value.endswith("000Z") else value
    finally:
        cursor.close()


def _write_json(path, value):
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    temporary = target.with_name(target.name + ".tmp")
    encoded = json.dumps(value, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode("ascii")
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(encoded)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, target)
        directory = os.open(str(target.parent), os.O_RDONLY)
        try:
            os.fsync(directory)
        finally:
            os.close(directory)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def _emit(output, value):
    if callable(output):
        output(value)
    else:
        output.write(json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n")


def run(argv, source_factory=_source_connection, producer_factory=_kafka_producer, output=None):
    parser = argparse.ArgumentParser(prog="evidence-migration")
    commands = parser.add_subparsers(dest="command", required=True)
    snapshot = commands.add_parser("snapshot", help="create the payload-free frozen source artifact")
    snapshot.add_argument("--manifest-id", required=True)
    snapshot.add_argument("--artifact", required=True)
    snapshot.add_argument("--core-ownership-gaps", help="exact payload-free identities of rows with unavailable Core ownership")
    snapshot.add_argument("--archive-only", action="store_true", help="require every frozen source row to be an ownership gap; publish no Kafka records")
    publish = commands.add_parser("publish", help="reread and publish an activated frozen artifact")
    publish.add_argument("--artifact", required=True)
    publish.add_argument("--receipt", required=True)
    publish.add_argument("--checkpoint", required=True)
    publish.add_argument("--producer-instance-id", required=True)
    publish.add_argument("--timeout-seconds", type=float, default=30)
    publish.add_argument("--core-ownership-gaps", help="same exact gap list used for snapshot")
    publish.add_argument("--archive-only", action="store_true", help="require every frozen source row to be an ownership gap; publish no Kafka records")
    args = parser.parse_args(argv)
    output = sys.stdout if output is None else output
    core_ownership_gaps = _load_core_ownership_gaps(args.core_ownership_gaps)

    if args.command == "snapshot":
        connection = source_factory()
        try:
            source_snapshot_at = _begin_readonly_snapshot(connection)
            rows = read_event_snapshot(connection, source_snapshot_at)
            artifact, _ = issue_manifest(args.manifest_id, source_snapshot_at, rows, core_ownership_gaps,
                                         archive_only=args.archive_only)
            _write_json(args.artifact, artifact)
            _emit(output, {"manifest_id": artifact["manifest_id"], "entry_count": artifact["entry_count"], "entries_digest": artifact["entries_digest"]})
        finally:
            try:
                connection.rollback()
            finally:
                connection.close()
        return 0

    manifest, entries = load_active_artifact(args.artifact, args.receipt)
    connection = source_factory()
    try:
        _begin_readonly_snapshot(connection)
        rows = read_event_snapshot(connection, manifest["source_snapshot_at"])
        events = verify_frozen_entries(rows, manifest, entries, core_ownership_gaps,
                                       archive_only=args.archive_only)
    finally:
        try:
            connection.rollback()
        finally:
            connection.close()

    producer = producer_factory() if any(entry["classification"] == "publish" for entry in entries) else None
    try:
        emitted = publish_encoded_entries(
            manifest, entries, events, args.checkpoint, producer, "openbkn.evidence.v1",
            args.timeout_seconds, args.producer_instance_id,
        )
        if producer is not None:
            producer.flush(timeout=args.timeout_seconds)
    finally:
        if producer is not None:
            producer.close(timeout=args.timeout_seconds)
    _emit(output, {"manifest_id": manifest["manifest_id"], "published_count": len(emitted), "completed": True})
    return 0


def main():
    try:
        return run(sys.argv[1:])
    except Exception as error:
        print(f"Evidence migration failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
