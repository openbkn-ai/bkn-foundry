#!/usr/bin/env python3
"""Regression tests for the BKN Trace OpenBKN license-header checker."""

from __future__ import annotations

import importlib.util
import unittest
from pathlib import Path


SCRIPT = Path(__file__).with_name("check_license_headers.py")


def load_checker():
    spec = importlib.util.spec_from_file_location("check_license_headers", SCRIPT)
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class LicenseHeaderCheckerTest(unittest.TestCase):
    def test_reports_missing_required_marker_for_go_file(self):
        checker = load_checker()
        path = Path("bkn-trace/agent-observability/main.go")

        errors = checker.validate_content(path, "package main\n")

        self.assertEqual(errors, ["missing Copyright (c) 2026 OpenBKN"])

    def test_accepts_openbkn_go_header(self):
        checker = load_checker()
        path = Path("bkn-trace/agent-observability/main.go")
        content = """// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package main
"""

        self.assertEqual(checker.validate_content(path, content), [])

    def test_excludes_immutable_migration_sql(self):
        checker = load_checker()

        self.assertFalse(
            checker.is_commentable(
                Path("bkn-trace/agent-observability/migrations/mariadb/v013/init.sql")
            )
        )


if __name__ == "__main__":
    unittest.main()
