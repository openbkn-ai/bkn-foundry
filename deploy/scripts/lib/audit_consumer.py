#!/usr/bin/env python3
# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Prepare and validate the complete installer's System Audit deployment."""

import base64
import copy
import json
import subprocess
import sys


def preserved_values(values):
    values = values or {}
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


def defaults(brokers, mechanism, secret):
    connection = {"enabled": True, "brokers": [brokers] if brokers else [],
                  "saslMechanism": mechanism,
                  "existingSecret": {"name": secret, "usernameKey": "username", "passwordKey": "password"}}
    consumer = copy.deepcopy(connection)
    consumer.update(topic="openbkn.audit.v1", consumerGroup="bkn-trace-audit-ledger-v1")
    return {"core": {"autoMigrate": True}, "kafkaConsumers": {"audit": consumer},
            "auditPublisher": connection}


def preserved_system_values(values):
    values = values or {}
    result = preserved_values(values)
    publisher = values.get("auditPublisher", {})
    keys = ("enabled", "environment", "existingSecret")
    result["auditPublisher"] = {key: copy.deepcopy(publisher[key]) for key in keys if key in publisher}
    for section in (result.get("kafkaConsumers", {}).get("audit", {}), result["auditPublisher"]):
        # Helm user values include defaults emitted by an earlier installer.
        # Resolve MQ-derived connections from today's config/CLI, not that copy.
        section.pop("brokers", None)
        section.pop("saslMechanism", None)
        if isinstance(section.get("existingSecret"), dict):
            section["existingSecret"] = {key: section["existingSecret"][key] for key in
                                         ("name", "usernameKey", "passwordKey") if key in section["existingSecret"]}
        # A formerly disabled component is a deployment gap in a complete
        # platform install. Keep useful connection fields, not empty defaults.
        if section.get("enabled") is False:
            for key in list(section):
                if key == "enabled" or not section[key] or (key == "existingSecret" and not section[key].get("name")):
                    del section[key]
    return result


def rendered_system_values(rendered):
    # Helm has already merged all input files/CLI. release.config omits Chart
    # defaults; combine those maps only, without reimplementing input priority.
    def merge(base, overrides):
        result = copy.deepcopy(base)
        for key, value in overrides.items():
            if isinstance(value, dict) and isinstance(result.get(key), dict):
                result[key] = merge(result[key], value)
            else:
                result[key] = copy.deepcopy(value)
        return result
    return merge(rendered.get("chart", {}).get("values", {}), rendered.get("config", {}))


def same_system_config(desired, installed):
    def config(values):
        core = values.get("core", {})
        return {"consumer": values.get("kafkaConsumers", {}).get("audit", {}),
                "publisher": values.get("auditPublisher", {}),
                "core": {key: core.get(key) for key in ("store", "autoMigrate", "mariadb")}}
    return config(desired) == config(installed or {})


def validate_system(values, namespace):
    consumer = values.get("kafkaConsumers", {}).get("audit", {})
    publisher = values.get("auditPublisher", {})
    for name, section in (("Audit consumer", consumer), ("Core Audit publisher", publisher)):
        if section.get("enabled") is not True:
            raise ValueError(f"{name} must be enabled for a complete OpenBKN installation")
        brokers = section.get("brokers")
        if not isinstance(brokers, list) or not brokers or not all(isinstance(b, str) and b.strip() for b in brokers):
            raise ValueError(f"{name} requires Kafka brokers")
    if not consumer.get("consumerGroup") or consumer.get("topic") != "openbkn.audit.v1":
        raise ValueError("Audit consumer requires an independent group and openbkn.audit.v1 topic")
    if publisher.get("saslMechanism") != "PLAIN":
        raise ValueError("Core Audit publisher requires the existing PLAIN Kafka path")
    if publisher.get("environment") not in ("development", "test", "staging", "production"):
        raise ValueError("auditPublisher.environment must explicitly identify development/test/staging/production")
    consumer_secret = consumer.get("existingSecret", {})
    publisher_secret = publisher.get("existingSecret", {})
    if publisher.get("environment") == "production" and publisher_secret.get("name") == consumer_secret.get("name"):
        raise ValueError("production Core Audit publisher requires an independent approved write Secret reference")
    core = values.get("core", {})
    if core.get("store") != "mariadb" or core.get("autoMigrate") is not True:
        raise ValueError("System Audit requires Core MariaDB and autoMigrate=true")
    validate(values, namespace)
    check_secret(namespace, publisher_secret.get("name"),
                 [publisher_secret.get("usernameKey", "username"), publisher_secret.get("passwordKey", "password")],
                 component="Core Audit publisher")


# Run the existing bundled Kafka CLI with the consumer's credentials. All
# secret material travels over stdin, never command arguments or error logs.
# The command creates only a missing Audit topic and never changes existing
# configurations, partitions, retention, ACLs or consumer offsets.
TOPIC_SCRIPT = r'''
set -euo pipefail
umask 077
client_config=$(mktemp)
trap 'rm -f "$client_config"' EXIT
cat >"$client_config"
tools=$2
topics=$("$tools/kafka-topics.sh" --bootstrap-server "$1" --command-config "$client_config" --list)
if ! printf '%s\n' "$topics" | grep -Fxq openbkn.audit.v1; then
    "$tools/kafka-topics.sh" --bootstrap-server "$1" --command-config "$client_config" \
        --create --if-not-exists --topic openbkn.audit.v1 --partitions 1 --replication-factor 1 \
        --config message.timestamp.type=LogAppendTime >/dev/null
fi
settings=$("$tools/kafka-configs.sh" --bootstrap-server "$1" --command-config "$client_config" \
    --entity-type topics --entity-name openbkn.audit.v1 --describe --all)
printf '%s\n' "$settings" | grep -Eq '(^|[,[:space:]])message.timestamp.type=LogAppendTime([,[:space:]]|$)'
'''


def prepare_topic(values, namespace, kafka_namespace, kafka_release):
    consumer = values["kafkaConsumers"]["audit"]
    secret = consumer["existingSecret"]
    result = subprocess.run(["kubectl", "get", "secret", secret["name"], "-n", namespace, "-o", "json",
                             "--request-timeout=10s"], capture_output=True, text=True, timeout=15)
    if result.returncode:
        raise ValueError("Audit topic preparation cannot read the consumer Secret")
    try:
        data = json.loads(result.stdout)["data"]
        username = base64.b64decode(data[secret["usernameKey"]], validate=True).decode()
        password = base64.b64decode(data[secret["passwordKey"]], validate=True).decode()
    except (KeyError, ValueError, UnicodeError):
        raise ValueError("Audit topic preparation requires valid consumer credentials") from None
    if not username or not password or any(c in username + password for c in "\r\n\x00"):
        raise ValueError("Audit topic preparation requires nonempty single-line consumer credentials")
    mechanism = consumer["saslMechanism"]
    if mechanism not in ("PLAIN", "SCRAM-SHA-256", "SCRAM-SHA-512"):
        raise ValueError("Audit topic preparation requires a supported Kafka SASL mechanism")
    module = "plain.PlainLoginModule" if mechanism == "PLAIN" else "scram.ScramLoginModule"
    jaas = f'org.apache.kafka.common.security.{module} required username={json.dumps(username, ensure_ascii=False)} password={json.dumps(password, ensure_ascii=False)};'
    properties = f"security.protocol=SASL_PLAINTEXT\nsasl.mechanism={mechanism}\n"
    properties += "sasl.jaas.config=" + jaas.replace("\\", "\\\\") + "\nrequest.timeout.ms=10000\ndefault.api.timeout.ms=30000\n"
    pods = subprocess.run(["kubectl", "get", "pods", "-n", kafka_namespace, "-l",
                           f"app.kubernetes.io/instance={kafka_release},app.kubernetes.io/component=broker",
                           "-o", "json", "--request-timeout=10s"], capture_output=True, text=True, timeout=15)
    items = json.loads(pods.stdout).get("items", []) if pods.returncode == 0 else []
    running = [p["metadata"]["name"] for p in items if p.get("status", {}).get("phase") == "Running"]
    if not running:
        raise ValueError("Audit topic preparation requires the existing bundled Kafka broker tooling; external deployments must provide reachable tooling")
    command = ["kubectl", "exec", "-i", "-n", kafka_namespace, running[0], "--", "bash", "-c",
               TOPIC_SCRIPT, "audit-topic", ",".join(consumer["brokers"]), "/opt/bitnami/kafka/bin"]
    result = subprocess.run(command, input=properties, capture_output=True, text=True, timeout=120)
    if result.returncode:
        raise ValueError("Audit topic preflight failed: verify consumer authentication/topic permissions and LogAppendTime; an existing CreateTime topic is not modified")


def check_secret(namespace, name, keys, component="Audit consumer"):
    if not name:
        raise ValueError("{} requires an existing Secret reference".format(component))
    result = subprocess.run(
        ["kubectl", "get", "secret", name, "-n", namespace, "-o", "json", "--request-timeout=10s"],
        capture_output=True, text=True, check=False, timeout=15,
    )
    if result.returncode:
        raise ValueError(f"{component} cannot read Secret {namespace}/{name}; create it before installation")
    data = json.loads(result.stdout).get("data", {})
    for key in keys:
        if not key or not data.get(key):
            raise ValueError(f"{component} Secret {namespace}/{name} is missing a non-empty key {key}")


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
        action = sys.argv[1]
        if action == "defaults":
            json.dump(defaults(*sys.argv[2:5]), sys.stdout)
            return 0
        values = json.load(sys.stdin)
        if action == "preserve-system":
            json.dump(preserved_system_values(values), sys.stdout)
        elif action == "preserve":
            json.dump(preserved_values(values), sys.stdout)
        elif action == "validate-system":
            validate_system(rendered_system_values(values), sys.argv[2])
        elif action == "prepare-topic":
            prepare_topic(rendered_system_values(values), *sys.argv[2:5])
        elif action == "same-system":
            with open(sys.argv[2]) as installed:
                return 0 if same_system_config(rendered_system_values(values), json.load(installed)) else 1
        else:
            validate(values.get("config", {}), sys.argv[2])
    except subprocess.TimeoutExpired:
        message = "Kafka topic preparation timed out" if sys.argv[1] == "prepare-topic" else "Secret lookup timed out"
        print(f"Audit consumer preflight: {message}; check cluster connectivity before installation", file=sys.stderr)
        return 1
    except (ValueError, OSError) as error:
        print(f"Audit consumer preflight: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
