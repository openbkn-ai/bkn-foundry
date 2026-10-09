#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Keep Agent Evidence deployment choices and validate existing credentials."""

import json
import subprocess
import sys
from urllib.parse import urlparse

from audit_consumer import check_secret


def preserved_values(values):
    if values is None:
        return {}
    publisher = values.get("observability", {}).get("evidencePublisher")
    if not isinstance(publisher, dict):
        return {}
    # Saved connection values may be old installer-forced defaults (including
    # the retired authenticated 8080 route), not operator choices. Resolve them
    # from today's installer/config/CLI. Keep capture and sizing choices only.
    keys = ("enabled", "queueMaxRecords", "queueMaxBytes", "maxRecordBytes",
            "maxAgeS", "maxAttempts", "retryBackoffMs", "drainTimeoutS")
    result = {key: publisher[key] for key in keys if key in publisher}
    return {"observability": {"evidencePublisher": result}}


def defaults(pairs):
    values = {}
    for pair in pairs:
        if not pair:
            continue
        key, value = pair.split("=", 1)
        target = values
        parts = key.split(".")
        for part in parts[:-1]:
            target = target.setdefault(part, {})
        # The only boolean in the supported publisher defaults is enabled.
        target[parts[-1]] = value == "true" if parts[-1] == "enabled" else value
    return values


def valid_broker(broker):
    host, separator, port = broker.rpartition(":")
    if not separator or not host or not port.isdecimal():
        return False
    if host.startswith("[") and host.endswith("]"):
        host = host[1:-1]
    elif ":" in host:
        return False
    return bool(host) and 1 <= int(port) <= 65535


def validate(values, namespace):
    publisher = values.get("observability", {}).get("evidencePublisher", {})
    enabled = publisher.get("enabled", False)
    if not isinstance(enabled, bool):
        raise ValueError("observability.evidencePublisher.enabled must be a boolean")
    if not enabled:
        return
    brokers = publisher.get("brokers", "")
    if not isinstance(brokers, str) or not all(valid_broker(part.strip()) for part in brokers.split(",")):
        raise ValueError("Agent Evidence publisher requires valid Kafka host:port brokers")
    # Mirror EvidenceKafkaConfig.validate bounds. Helm numeric defaults and
    # --set-string are both supported; do not accept fractional integer limits.
    for key, lower, upper, integer in (
        ("queueMaxRecords", 1, 1_000_000, True),
        ("queueMaxBytes", 1, 1 << 30, True),
        ("maxRecordBytes", 1, 1 << 20, True),
        ("maxAgeS", 0.1, 3600, False),
        ("maxAttempts", 1, 10, True),
        ("retryBackoffMs", 0, 60000, False),
        ("drainTimeoutS", 0.1, 60, False),
    ):
        raw = publisher.get(key)
        try:
            if isinstance(raw, bool):
                raise ValueError()
            value = int(str(raw)) if integer else float(raw)
            if not lower <= value <= upper:
                raise ValueError()
        except (ValueError, TypeError, OverflowError):
            raise ValueError("observability.evidencePublisher.{} is outside runtime bounds".format(key)) from None
    for key in ("policyURL", "configurationURL", "heartbeatURL", "ackURLBase"):
        value = publisher.get("traceAdmission", {}).get(key, "")
        parsed = urlparse(value)
        if parsed.scheme not in ("http", "https") or not parsed.netloc:
            raise ValueError("observability.evidencePublisher.traceAdmission.{} requires a complete HTTP(S) URL".format(key))
    check_secret(namespace, publisher.get("credentialsSecretName"),
                 [publisher.get("usernameSecretKey"), publisher.get("passwordSecretKey")],
                 component="Agent Evidence publisher")


def main():
    try:
        if sys.argv[1] == "defaults":
            json.dump(defaults(sys.argv[2:]), sys.stdout)
        elif sys.argv[1] == "preserve":
            json.dump(preserved_values(json.load(sys.stdin)), sys.stdout)
        else:
            rendered = json.load(sys.stdin)
            # Helm's release.config contains overrides only. Include the chart
            # defaults when validating a fresh install or a partial values file.
            publisher = rendered.get("chart", {}).get("values", {}).get("observability", {}).get("evidencePublisher", {}).copy()
            publisher.update(rendered.get("config", {}).get("observability", {}).get("evidencePublisher", {}))
            # Helm merges maps recursively; current partial endpoint settings
            # do not remove the other three chart defaults.
            admission = rendered.get("chart", {}).get("values", {}).get("observability", {}).get("evidencePublisher", {}).get("traceAdmission", {}).copy()
            admission.update(publisher.get("traceAdmission", {}))
            publisher["traceAdmission"] = admission
            validate({"observability": {"evidencePublisher": publisher}}, sys.argv[2])
    except subprocess.TimeoutExpired:
        print("Agent Evidence preflight: Secret lookup timed out", file=sys.stderr)
        return 1
    except (ValueError, OSError) as error:
        print("Agent Evidence preflight: {}".format(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
