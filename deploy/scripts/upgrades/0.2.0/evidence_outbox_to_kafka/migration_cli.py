"""Small split-capability CLI for the one-time Evidence migration bridge."""

import argparse
import json
import os
import sys
from pathlib import Path

from bridge import load_active_artifact, publish_encoded_entries
from manifest import ManifestError
from snapshot import issue_manifest, read_event_snapshot, verify_frozen_entries

MAX_KAFKA_REQUEST_SIZE = 2_097_152


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
    publish = commands.add_parser("publish", help="reread and publish an activated frozen artifact")
    publish.add_argument("--artifact", required=True)
    publish.add_argument("--receipt", required=True)
    publish.add_argument("--checkpoint", required=True)
    publish.add_argument("--producer-instance-id", required=True)
    publish.add_argument("--timeout-seconds", type=float, default=30)
    args = parser.parse_args(argv)
    output = sys.stdout if output is None else output

    if args.command == "snapshot":
        connection = source_factory()
        try:
            source_snapshot_at = _begin_readonly_snapshot(connection)
            rows = read_event_snapshot(connection, source_snapshot_at)
            artifact, _ = issue_manifest(args.manifest_id, source_snapshot_at, rows)
            _write_json(args.artifact, artifact)
            _emit(output, {"manifest_id": args.manifest_id, "entry_count": artifact["entry_count"], "entries_digest": artifact["entries_digest"]})
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
        events = verify_frozen_entries(rows, manifest, entries)
    finally:
        try:
            connection.rollback()
        finally:
            connection.close()

    producer = producer_factory()
    try:
        emitted = publish_encoded_entries(
            manifest, entries, events, args.checkpoint, producer, "openbkn.evidence.v1",
            args.timeout_seconds, args.producer_instance_id,
        )
        producer.flush(timeout=args.timeout_seconds)
    finally:
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
