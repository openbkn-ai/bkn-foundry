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
    publisher = values.get("observability", {}).get("evidencePublisher")
    if not isinstance(publisher, dict):
        return {}
    keys = ("enabled", "brokers", "credentialsSecretName", "usernameSecretKey",
            "passwordSecretKey", "queueMaxRecords", "queueMaxBytes", "maxRecordBytes",
            "maxAgeS", "maxAttempts", "retryBackoffMs", "drainTimeoutS")
    result = {key: publisher[key] for key in keys if key in publisher}
    admission = publisher.get("traceAdmission")
    if isinstance(admission, dict):
        result["traceAdmission"] = {key: admission[key] for key in
                                    ("policyURL", "configurationURL", "heartbeatURL", "ackURLBase")
                                    if key in admission}
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


def validate(values, namespace):
    publisher = values.get("observability", {}).get("evidencePublisher", {})
    enabled = publisher.get("enabled", False)
    if not isinstance(enabled, bool):
        raise ValueError("observability.evidencePublisher.enabled must be a boolean")
    if not enabled:
        return
    if not publisher.get("brokers"):
        raise ValueError("Agent Evidence publisher requires Kafka brokers")
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
