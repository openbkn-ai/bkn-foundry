"""CI must fail closed on lock/test-dependency errors, without contacting PyPI."""

import os
import subprocess
from pathlib import Path

import pytest
import yaml
from packaging.requirements import Requirement
from packaging.utils import canonicalize_name

ROOT = Path(__file__).resolve().parents[4]
WORKFLOW = ROOT / ".github/workflows/reusable-release-verify.yml"


def active_requirements(path):
    for line in path.read_text().splitlines():
        value = line.strip().removesuffix("\\").strip()
        if not value or value.startswith(("#", "--")):
            continue
        requirement = Requirement(value)
        if requirement.marker is None or requirement.marker.evaluate():
            yield requirement


def test_runtime_and_test_locks_match_declared_ranges_and_each_other():
    service = ROOT / "infra/bkn-agent"
    runtime = {
        canonicalize_name(r.name): str(r.specifier)
        for r in active_requirements(service / "requirements.lock")
    }
    testing = {
        canonicalize_name(r.name): str(r.specifier)
        for r in active_requirements(service / "test-requirements.lock")
    }
    assert all(testing[name] == pin for name, pin in runtime.items())
    assert "pytest" not in runtime
    assert "aiosqlite" not in runtime
    for source in ("requirements.txt", "test-requirements.txt"):
        for requirement in active_requirements(service / source):
            pin = testing[canonicalize_name(requirement.name)]
            assert pin.startswith("==")
            assert pin[2:] in requirement.specifier, (
                f"lock does not satisfy {requirement}"
            )


@pytest.mark.parametrize(
    "mode", ["locked", "plain", "missing_test_lock", "failed_test_install"]
)
def test_ci_dependency_selection_and_failure_propagation(tmp_path, mode):
    workflow = yaml.safe_load(WORKFLOW.read_text())
    script = next(
        s["run"]
        for s in workflow["jobs"]["verify"]["steps"]
        if s.get("name") == "Run Python test"
    )
    (tmp_path / "requirements.txt").touch()
    (tmp_path / "test-requirements.txt").touch()
    if mode != "plain":
        (tmp_path / "requirements.lock").touch()
    if mode not in {"plain", "missing_test_lock"}:
        (tmp_path / "test-requirements.lock").touch()
    commands = tmp_path / "commands"
    fake_bin = tmp_path / "bin"
    fake_bin.mkdir()
    pip = fake_bin / "pip"
    pip.write_text(
        '#!/bin/sh\nprintf "pip %s\\n" "$*" >> "$TASK_COMMAND_LOG"\ncase "$*" in *test-requirements*) test "$TASK_FAIL_INSTALL" != 1 ;; esac\n'
    )
    runner = fake_bin / "pytest"
    runner.write_text('#!/bin/sh\nprintf "pytest\\n" >> "$TASK_COMMAND_LOG"\n')
    pip.chmod(0o755)
    runner.chmod(0o755)
    result = subprocess.run(
        ["bash", "-c", script],
        cwd=tmp_path,
        capture_output=True,
        text=True,
        env={
            **os.environ,
            "PATH": f"{fake_bin}:{os.environ['PATH']}",
            "TASK_COMMAND_LOG": str(commands),
            "TASK_FAIL_INSTALL": "1" if mode == "failed_test_install" else "0",
        },
    )
    calls = commands.read_text().splitlines()
    if mode in {"missing_test_lock", "failed_test_install"}:
        assert result.returncode != 0
        assert "pytest" not in calls
    else:
        assert result.returncode == 0, result.stderr
        expected = (
            [
                "pip install --require-hashes -r requirements.lock",
                "pip install --require-hashes -r test-requirements.lock",
            ]
            if mode == "locked"
            else [
                "pip install -r requirements.txt",
                "pip install -r test-requirements.txt",
            ]
        )
        assert calls == [*expected, "pytest"]
