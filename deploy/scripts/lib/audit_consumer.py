#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Preserve Audit consumer values and check its existing Secret references."""

import json
import subprocess
import sys


def preserved_values(values):
    audit = values.get("kafkaConsumers", {}).get("audit")
    if not isinstance(audit, dict):
        return {}
    # Carry only the supported consumer configuration, never inline credentials
    # or unrelated old chart defaults. Current values/--set still take precedence.
    keys = ("enabled", "brokers", "topic", "consumerGroup", "saslMechanism", "existingSecret")
    preserved = {key: audit[key] for key in keys if key in audit}
    if isinstance(preserved.get("existingSecret"), dict):
        secret = preserved["existingSecret"]
        preserved["existingSecret"] = {key: secret[key] for key in
                                       ("name", "usernameKey", "passwordKey") if key in secret}
    return {"kafkaConsumers": {"audit": preserved}}


def check_secret(namespace, name, keys):
    if not name:
        raise ValueError("Audit consumer requires an existing Secret reference")
    result = subprocess.run(
        ["kubectl", "get", "secret", name, "-n", namespace, "-o", "json", "--request-timeout=10s"],
        capture_output=True, text=True, check=False, timeout=15,
    )
    if result.returncode:
        raise ValueError(f"Audit consumer cannot read Secret {namespace}/{name}; create it before installation")
    data = json.loads(result.stdout).get("data", {})
    for key in keys:
        if not key or not data.get(key):
            raise ValueError(f"Audit consumer Secret {namespace}/{name} is missing a non-empty key {key}")


def validate(values, namespace):
    audit = values.get("kafkaConsumers", {}).get("audit", {})
    if not audit.get("enabled"):
        return
    # Helm renders and validates brokers/group/topic/SASL/Core before this step.
    secret = audit.get("existingSecret", {})
    check_secret(namespace, secret.get("name"),
                 [secret.get("usernameKey", "username"), secret.get("passwordKey", "password")])
    mariadb = values.get("core", {}).get("mariadb", {})
    check_secret(namespace, mariadb.get("existingSecret", "bkn-trace-core-mariadb"),
                 [mariadb.get("dsnKey", "dsn")])


def main():
    try:
        values = json.load(sys.stdin)
        if sys.argv[1] == "preserve":
            json.dump(preserved_values(values), sys.stdout)
        else:
            validate(values.get("config", {}), sys.argv[2])
    except subprocess.TimeoutExpired:
        print("Audit consumer preflight: Secret lookup timed out; check cluster connectivity before installation", file=sys.stderr)
        return 1
    except (ValueError, OSError) as error:
        print(f"Audit consumer preflight: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
