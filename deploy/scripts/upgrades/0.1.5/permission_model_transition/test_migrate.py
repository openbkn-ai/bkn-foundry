# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import argparse
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import migrate


class StepRegistryTest(unittest.TestCase):
    def test_registers_bkn_before_authorization(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            manifest = root / "manifest.json"
            executable = root / "authz-migrate"
            manifest.write_text("{}", encoding="utf-8")
            executable.write_text("binary", encoding="utf-8")
            executable.chmod(0o700)
            args = argparse.Namespace(
                command="dry-run",
                manifest=str(manifest),
                authz_config="",
                authz_migrator=str(executable),
            )

            steps = migrate.build_steps(args, root / "reports")

        self.assertEqual(["bkn-data", "authorization"], [step.name for step in steps])
        self.assertIn("bkn_data.py", steps[0].command[1])
        self.assertEqual("dry-run", steps[1].command[2])


class OrchestrationTest(unittest.TestCase):
    def test_apply_requires_the_stop_replica_snapshot(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            args = argparse.Namespace(
                command="apply",
                source_version="0.1.4",
                state_file=str(root / "missing.tsv"),
                report_dir=str(root / "reports"),
            )

            with self.assertRaisesRegex(
                migrate.OrchestrationError, "run the unified stop command first"
            ):
                migrate.run_migration(args)

    def test_runs_registered_steps_in_order_and_writes_summary(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = root / "state.tsv"
            state.write_text("replica snapshot", encoding="utf-8")
            report_dir = root / "reports"
            steps = [
                migrate.Step("bkn-data", ("bkn",), report_dir / "01.json"),
                migrate.Step("authorization", ("safe",), report_dir / "02.json"),
            ]
            args = argparse.Namespace(
                command="apply",
                source_version="0.1.4",
                state_file=str(state),
                report_dir=str(report_dir),
            )
            calls = []

            def complete(step):
                calls.append(step.name)
                step.report_path.write_text("{}", encoding="utf-8")

            with patch.object(migrate, "require_stopped_workloads") as stopped, patch.object(
                migrate, "build_steps", return_value=steps
            ), patch.object(migrate, "run_step", side_effect=complete):
                self.assertEqual(0, migrate.run_migration(args))

            stopped.assert_called_once_with(args)
            self.assertEqual(["bkn-data", "authorization"], calls)
            summary = json.loads((report_dir / "summary.json").read_text())
            self.assertTrue(all(step["completed"] for step in summary["steps"]))

    def test_failure_stops_before_the_next_step_and_records_it(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            report_dir = root / "reports"
            steps = [
                migrate.Step("bkn-data", ("bkn",), report_dir / "01.json"),
                migrate.Step("authorization", ("safe",), report_dir / "02.json"),
            ]
            args = argparse.Namespace(
                command="dry-run",
                source_version="0.1.4",
                state_file=str(root / "unused.tsv"),
                report_dir=str(report_dir),
            )

            with patch.object(migrate, "build_steps", return_value=steps), patch.object(
                migrate,
                "run_step",
                side_effect=migrate.OrchestrationError("bkn failed"),
            ) as run_step:
                with self.assertRaisesRegex(migrate.OrchestrationError, "bkn failed"):
                    migrate.run_migration(args)

            run_step.assert_called_once_with(steps[0])
            summary = json.loads((report_dir / "summary.json").read_text())
            self.assertEqual("bkn failed", summary["error"])
            self.assertFalse(summary["steps"][1]["completed"])

    def test_rejects_every_source_release_except_0_1_4(self):
        self.assertEqual("0.1.4", migrate.validate_source_version("v0.1.4"))
        with self.assertRaisesRegex(migrate.OrchestrationError, "only migrates 0.1.4"):
            migrate.validate_source_version("0.1.5")


if __name__ == "__main__":
    unittest.main()
