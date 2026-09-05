# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import importlib.util
import unittest
from pathlib import Path
from unittest.mock import patch

SCRIPT_PATH = Path(__file__).with_name("script.py")
SPEC = importlib.util.spec_from_file_location("migrate_kn_proxy_cli", SCRIPT_PATH)
if SPEC is None or SPEC.loader is None:
    raise RuntimeError(f"cannot load migration script from {SCRIPT_PATH}")
MIGRATION_SCRIPT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MIGRATION_SCRIPT)
build_report = MIGRATION_SCRIPT.build_report


class BuildReportTest(unittest.TestCase):
    @patch.object(MIGRATION_SCRIPT, "request_json")
    def test_dry_run_never_calls_sync(self, request_json):
        request_json.side_effect = [
            {"entries": [{"id": "kn-1"}], "total_count": 1},
            {
                "kn_id": "kn-1",
                "model_version": "sha256:abc",
                "sources": [{"resource_type": "resource", "resource_id": "r-1"}],
            },
        ]

        report = build_report("http://backend", "grantor-1", apply=False)

        self.assertEqual("dry-run", report["mode"])
        self.assertEqual("planned", report["networks"][0]["status"])
        self.assertEqual(2, request_json.call_count)

    @patch.object(MIGRATION_SCRIPT, "request_json")
    def test_apply_uses_serial_sync_result(self, request_json):
        request_json.side_effect = [
            {"entries": [{"id": "kn-1"}], "total_count": 1},
            {"kn_id": "kn-1", "model_version": "sha256:abc", "sources": []},
            {"proxy_account_id": "proxy-1", "sync_status": "ready"},
        ]

        report = build_report("http://backend", "grantor-1", apply=True)

        self.assertEqual("apply", report["mode"])
        self.assertEqual("ready", report["networks"][0]["status"])
        self.assertEqual("POST", request_json.call_args_list[-1].kwargs["method"])


if __name__ == "__main__":
    unittest.main()
