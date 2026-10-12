import contextlib
import io
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

import cli
import snapshot


class CLITests(unittest.TestCase):
    def test_plan_and_verify_roundtrip_empty_snapshot(self):
        with tempfile.TemporaryDirectory() as tmp:
            source, output = Path(tmp) / "source", Path(tmp) / "plan"
            snapshot.save_snapshot(source, [], "instance", {})
            with contextlib.redirect_stdout(io.StringIO()) as stdout:
                status = cli.main(["plan", "--source", str(source), "--output", str(output), "--environment", "test", "--validation-time", "2026-10-06T00:00:00Z", "--native-validator", "unused"])
                verified = cli.main(["verify", "--plan", str(output)])
            self.assertEqual((status, verified), (0, 0))
            self.assertNotIn("payload", stdout.getvalue())

    def test_error_summary_does_not_expose_input(self):
        with contextlib.redirect_stderr(io.StringIO()) as stderr:
            status = cli.main(["verify", "--plan", "/nonexistent/private-user-name"])
        self.assertEqual(status, 1)
        self.assertNotIn("private-user-name", stderr.getvalue())

    def test_span_cli_uses_actual_receipt_fields(self):
        with tempfile.TemporaryDirectory() as tmp:
            profile = Path(tmp) / "target.json"
            profile.write_text('{}')
            receipt = {"stage": "native_span_readback_verified", "created_count": 3, "verified_count": 3, "existing_verified_count": 0, "entries": [{}, {}, {}]}
            with patch("cli.apply_spans.apply", return_value=receipt), contextlib.redirect_stdout(io.StringIO()) as stdout:
                status = cli.main(["apply-spans", "--plan", tmp, "--expected-items-sha256", "items", "--expected-plan-sha256", "plan", "--expected-profile-sha256", "profile", "--target-profile", str(profile), "--receipt", str(Path(tmp) / "receipt.json"), "--mode", "qualification"])
            self.assertEqual(status, 0)
            self.assertIn('"verified_count": 3', stdout.getvalue())


if __name__ == "__main__":
    unittest.main()
