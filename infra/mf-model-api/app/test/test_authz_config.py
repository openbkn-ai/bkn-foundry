"""Tests for the AUTHZ_PROVIDER startup validation."""
import os
import unittest
from unittest import mock

from app.core.config import authz_settings, base_config, validate_authz_config


class ValidateAuthzConfigTest(unittest.TestCase):
    """The retired ISF fallback has to surface as a startup failure."""

    def _run(self, provider, safe_url, auth_enabled=True):
        env = {"AUTHZ_PROVIDER": provider, "BKN_SAFE_URL": safe_url}
        with mock.patch.dict(os.environ, env, clear=False), \
                mock.patch.object(base_config, "AUTH_ENABLED", auth_enabled):
            validate_authz_config()

    def test_bkn_safe_with_url_is_accepted(self):
        self._run("bkn-safe", "http://bkn-safe:3000")

    def test_shadow_with_url_is_accepted(self):
        self._run("shadow", "http://bkn-safe:3000")

    def test_isf_is_accepted_as_escape_hatch(self):
        self._run("isf", "")

    def test_bkn_safe_without_url_is_rejected(self):
        with self.assertRaises(RuntimeError):
            self._run("bkn-safe", "  ")

    def test_shadow_without_url_is_rejected(self):
        with self.assertRaises(RuntimeError):
            self._run("shadow", "")

    def test_unset_provider_is_accepted_with_a_warning(self):
        # Refusing to start would turn an upgrade of a deployment that pins an
        # explicit empty value into a CrashLoopBackOff.
        self._run("", "http://bkn-safe:3000")

    def test_misspelled_provider_is_rejected(self):
        with self.assertRaises(RuntimeError):
            self._run("bkn_safe", "http://bkn-safe:3000")

    def test_padded_provider_matches_the_runtime_comparison(self):
        # A values file with a trailing space has to reach PermissionManager as
        # the same normalised value the startup check accepted, or the service
        # boots and then falls back to ISF on every decision.
        env = {"AUTHZ_PROVIDER": " bkn-safe ", "BKN_SAFE_URL": " http://bkn-safe:3000 "}
        with mock.patch.dict(os.environ, env, clear=False), \
                mock.patch.object(base_config, "AUTH_ENABLED", True):
            validate_authz_config()
            self.assertEqual(authz_settings(), ("bkn-safe", "http://bkn-safe:3000"))

    def test_disabled_auth_skips_validation(self):
        # No authorization backend is consulted at all, so a missing provider
        # is not a misconfiguration.
        self._run("", "", auth_enabled=False)


if __name__ == "__main__":
    unittest.main()
