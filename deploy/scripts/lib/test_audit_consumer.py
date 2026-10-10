#!/usr/bin/env python3
# Copyright openbkn.ai
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import copy
import json
import subprocess
import os
import tempfile
from pathlib import Path
import unittest
from unittest.mock import patch

import audit_consumer as audit


class SystemAuditTests(unittest.TestCase):
    def values(self):
        values = audit.defaults("broker:9092", "PLAIN", "existing-client")
        values["auditPublisher"]["environment"] = "test"
        values["core"] = {"store": "mariadb", "autoMigrate": True,
                          "mariadb": {"existingSecret": "core-db", "dsnKey": "dsn"}}
        return values

    def test_defaults_complete_both_sides_without_credentials(self):
        values = self.values()
        self.assertTrue(values["kafkaConsumers"]["audit"]["enabled"])
        self.assertTrue(values["auditPublisher"]["enabled"])
        self.assertEqual(values["kafkaConsumers"]["audit"]["consumerGroup"], "bkn-trace-audit-ledger-v1")
        self.assertNotIn("password", values["auditPublisher"])

    def test_environment_is_not_guessed(self):
        self.assertNotIn("environment", audit.defaults("broker:9092", "PLAIN", "existing-client")["auditPublisher"])

    def test_historical_disabled_empty_settings_do_not_block_defaults(self):
        values = self.values()
        for section in (values["kafkaConsumers"]["audit"], values["auditPublisher"]):
            section.update(enabled=False, brokers=[], saslMechanism="", existingSecret={"name": ""})
        saved = audit.preserved_system_values(values)
        self.assertNotIn("enabled", saved["kafkaConsumers"]["audit"])
        self.assertNotIn("brokers", saved["auditPublisher"])
        self.assertNotIn("existingSecret", saved["auditPublisher"])
        self.assertEqual(saved["auditPublisher"]["environment"], "test")

    def test_preserves_connections_but_never_inline_password(self):
        values = self.values()
        values["auditPublisher"]["password"] = "do-not-copy"
        saved = audit.preserved_system_values(values)
        self.assertEqual(saved["auditPublisher"]["brokers"], ["broker:9092"])
        self.assertNotIn("password", saved["auditPublisher"])
        self.assertNotIn("core", saved)

    @patch.object(audit, "check_secret")
    def test_validates_both_sides_and_database(self, check):
        audit.validate_system(self.values(), "openbkn")
        self.assertEqual(check.call_count, 3)

    @patch.object(audit, "check_secret")
    def test_current_disable_or_string_enablement_rejected(self, check):
        for component in ("consumer", "publisher"):
            for enabled in (False, "true"):
                values = self.values()
                section = values["auditPublisher"] if component == "publisher" else values["kafkaConsumers"]["audit"]
                section["enabled"] = enabled
                with self.subTest(component=component, enabled=enabled), self.assertRaises(ValueError):
                    audit.validate_system(values, "openbkn")

    @patch.object(audit, "check_secret")
    def test_missing_environment_unsupported_mechanism_and_core_rejected(self, check):
        for path, value in ((["auditPublisher", "environment"], ""),
                            (["auditPublisher", "saslMechanism"], "SCRAM-SHA-256"),
                            (["core", "autoMigrate"], False),
                            (["core", "store"], "memory")):
            values = self.values()
            values[path[0]][path[1]] = value
            with self.subTest(path=path), self.assertRaises(ValueError):
                audit.validate_system(values, "openbkn")

    @patch.object(audit, "check_secret")
    def test_production_does_not_assume_consumer_identity_is_publisher_identity(self, check):
        values = self.values()
        values["auditPublisher"]["environment"] = "production"
        with self.assertRaisesRegex(ValueError, "production.*independent"):
            audit.validate_system(values, "openbkn")
        values["auditPublisher"]["existingSecret"]["name"] = "approved-publisher"
        audit.validate_system(values, "openbkn")

    def test_rendered_defaults_and_partial_maps_use_helm_result(self):
        values = self.values()
        rendered = {"chart": {"values": values}, "config": {"auditPublisher": {"existingSecret": {"name": "custom"}}}}
        actual = audit.rendered_system_values(rendered)
        self.assertEqual(actual["auditPublisher"]["existingSecret"],
                         {"name": "custom", "usernameKey": "username", "passwordKey": "password"})

    def test_same_version_compares_all_effective_audit_fields(self):
        values = self.values()
        self.assertTrue(audit.same_system_config(values, copy.deepcopy(values)))
        for component, field, value in (("publisher", "environment", "staging"),
                                        ("consumer", "consumerGroup", "other"),
                                        ("publisher", "brokers", ["new:9092"])):
            changed = copy.deepcopy(values)
            section = changed["auditPublisher"] if component == "publisher" else changed["kafkaConsumers"]["audit"]
            section[field] = value
            self.assertFalse(audit.same_system_config(values, changed))
        values["auditPublisher"]["enabled"] = False
        self.assertFalse(audit.same_system_config(self.values(), values))

    def test_changed_ledger_secret_requires_reconciliation(self):
        values = self.values()
        changed = copy.deepcopy(values)
        changed["core"]["mariadb"]["existingSecret"] = "other-core-db"
        self.assertFalse(audit.same_system_config(values, changed))

    @patch.object(audit.subprocess, "run")
    def test_topic_check_uses_consumer_identity_without_credentials_in_arguments(self, run):
        run.side_effect = [subprocess.CompletedProcess([], 0, '{"data":{"username":"dGVzdA==","password":"c2VjcmV0"}}'),
                           subprocess.CompletedProcess([], 0, '{"items":[{"metadata":{"name":"kafka-broker-0"},"status":{"phase":"Running"}}]}'),
                           subprocess.CompletedProcess([], 0, '')]
        audit.prepare_topic(self.values(), "openbkn", "resource", "kafka")
        call = run.call_args
        self.assertNotIn("secret", json.dumps(call.args))
        self.assertIn("password=", call.kwargs["input"])
        self.assertIn("kafka-broker-0", call.args[0])
        self.assertIn("LogAppendTime", call.args[0][-4])

    @patch.object(audit.subprocess, "run")
    def test_unreadable_or_incompatible_topic_fails_without_echoing_credentials(self, run):
        run.side_effect = [subprocess.CompletedProcess([], 0, '{"data":{"username":"dGVzdA==","password":"c2VjcmV0"}}'),
                           subprocess.CompletedProcess([], 0, '{"items":[{"metadata":{"name":"kafka-broker-0"},"status":{"phase":"Running"}}]}'),
                           subprocess.CompletedProcess([], 1, '', 'sensitive broker error secret')]
        with self.assertRaisesRegex(ValueError, "LogAppendTime") as raised:
            audit.prepare_topic(self.values(), "openbkn", "resource", "kafka")
        self.assertNotIn("secret", str(raised.exception))

    @patch.object(audit.subprocess, "run")
    def test_missing_tools_or_invalid_credentials_fail_before_topic_command(self, run):
        run.side_effect = [subprocess.CompletedProcess([], 0, '{"data":{"username":"dGVzdA==","password":"c2VjcmV0"}}'),
                           subprocess.CompletedProcess([], 0, '{"items":[]}')]
        with self.assertRaisesRegex(ValueError, "Kafka broker tooling"):
            audit.prepare_topic(self.values(), "openbkn", "resource", "kafka")
        self.assertEqual(run.call_count, 2)


class TopicCommandTests(unittest.TestCase):
    def test_missing_existing_incompatible_and_unreachable_topics(self):
        # Execute the actual shell command against fake Kafka tools; no cluster
        # or broker writes. Assert existing topics are never altered.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            topics = root / "kafka-topics.sh"
            configs = root / "kafka-configs.sh"
            topics.write_text('''#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >>"$TEST_TOPIC_LOG"
case " $* " in
  *" --list "*)
    [[ "$TEST_TOPIC_STATE" != unreachable ]] || exit 1
    [[ "$TEST_TOPIC_STATE" == missing ]] || printf 'openbkn.audit.v1\\n' ;;
  *" --create "*) exit 0 ;;
  *) exit 1 ;;
esac
''')
            configs.write_text('''#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >>"$TEST_TOPIC_LOG"
if [[ "$TEST_TOPIC_STATE" == incompatible ]]; then
  printf 'message.timestamp.type=CreateTime sensitive=false\\n'
else
  printf 'message.timestamp.type=LogAppendTime sensitive=false\\n'
fi
''')
            topics.chmod(0o700)
            configs.chmod(0o700)
            log = root / "calls"
            for state in ("missing", "existing", "incompatible", "unreachable"):
                log.write_text("")
                env = dict(os.environ, TEST_TOPIC_STATE=state, TEST_TOPIC_LOG=str(log))
                result = subprocess.run(["bash", "-c", audit.TOPIC_SCRIPT, "audit-test", "broker:9092", directory],
                                        input="sasl.jaas.config=test-only-password\n", text=True, capture_output=True, env=env)
                with self.subTest(state=state):
                    self.assertEqual(result.returncode == 0, state in ("missing", "existing"))
                    calls = log.read_text()
                    self.assertEqual("--create" in calls, state == "missing")
                    self.assertNotIn("--alter", calls)
                    self.assertNotIn("test-only-password", calls + result.stdout + result.stderr)
                    for call in calls.splitlines():
                        self.assertIn("--command-config", call)


if __name__ == "__main__":
    unittest.main()
