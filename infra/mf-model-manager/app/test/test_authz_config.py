"""Tests for mandatory bkn-safe authorization configuration."""
import os
import unittest
from unittest import mock

from app.core.config import bkn_safe_url, validate_authz_config


class ValidateAuthzConfigTest(unittest.TestCase):
    def test_url_is_accepted_and_normalized(self):
        with mock.patch.dict(os.environ, {"BKN_SAFE_URL": " http://bkn-safe:3000 "}, clear=False):
            validate_authz_config()
            self.assertEqual(bkn_safe_url(), "http://bkn-safe:3000")

    def test_missing_url_is_rejected(self):
        with mock.patch.dict(os.environ, {"BKN_SAFE_URL": "  "}, clear=False), \
                self.assertRaises(RuntimeError):
            validate_authz_config()


if __name__ == "__main__":
    unittest.main()
