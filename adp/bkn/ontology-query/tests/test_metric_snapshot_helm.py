# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Check the rendered snapshot configuration consumed by EE's integer parser."""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest


class MetricSnapshotHelmTest(unittest.TestCase):
    chart = Path(__file__).resolve().parents[1] / "helm" / "ontology-query"
    defaults = {
        "MAX_ROWS": 10000,
        "MAX_ENTRY_BYTES": 16777216,
        "MAX_BYTES": 268435456,
        "MAX_ENTRIES": 128,
        "MAX_ACCOUNT_ENTRIES": 8,
        "TTL_SECONDS": 900,
    }

    def render(self, *arguments):
        return subprocess.run(
            [os.environ.get("HELM_BIN", "helm"), "template", "snapshot-test",
             str(self.chart), "--show-only", "templates/deployment.yaml", *arguments],
            check=True, capture_output=True, text=True,
        ).stdout

    def assert_snapshot_environment(self, rendered, expected):
        values = dict(re.findall(
            r'- name: BKN_METRIC_SNAPSHOT_(\w+)\s+value: "([^"\n]+)"', rendered,
        ))
        self.assertEqual(set(values), set(expected))
        for name, value in values.items():
            with self.subTest(variable=name):
                self.assertRegex(value, r"^[0-9]+$")
                self.assertEqual(int(value, 10), expected[name])

    def test_defaults_are_decimal_integer_strings(self):
        self.assert_snapshot_environment(self.render(), self.defaults)

    def test_large_values_from_yaml_remain_decimal_integer_strings(self):
        with tempfile.TemporaryDirectory() as directory:
            values = Path(directory) / "values.yaml"
            values.write_text(
                "metricResultSnapshot:\n"
                "  maxRows: 100000\n"
                "  maxEntryBytes: 1073741824\n"
                "  maxBytes: 17179869184\n"
                "  maxEntries: 100000\n"
                "  maxAccountEntries: 1000\n"
                "  ttlSeconds: 86400\n", encoding="utf-8",
            )
            self.assert_snapshot_environment(self.render("-f", str(values)), {
                "MAX_ROWS": 100000, "MAX_ENTRY_BYTES": 1073741824,
                "MAX_BYTES": 17179869184, "MAX_ENTRIES": 100000,
                "MAX_ACCOUNT_ENTRIES": 1000, "TTL_SECONDS": 86400,
            })

    def test_multiple_replicas_can_use_an_existing_cursor_secret(self):
        rendered = self.render(
            "--set", "replicaCount=3", "--set", "cursorEncryption.existingSecret=stable-cursor",
        )
        self.assertIn('name: "stable-cursor"', rendered)
        self.assert_snapshot_environment(rendered, self.defaults)


if __name__ == "__main__":
    unittest.main()
