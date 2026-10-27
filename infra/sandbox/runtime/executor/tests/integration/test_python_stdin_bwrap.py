"""Real Linux bwrap regression tests; requires existing user-namespace support.

Run explicitly with pytest tests/integration/test_python_stdin_bwrap.py.
No namespace or process boundary is mocked, and no isolation flag is removed.
"""

import ast
import asyncio
import json
import os
from pathlib import Path
import shutil
import sys

import pytest

from executor.infrastructure.config import settings
from executor.infrastructure.isolation.bwrap import BubblewrapRunner
from tests.unit.isolation.test_python_stdin import execution

pytestmark = pytest.mark.skipif(
    sys.platform != "linux" or shutil.which("bwrap") is None,
    reason="requires Linux and bubblewrap",
)


@pytest.fixture
def runner(tmp_path, monkeypatch):
    workspace = tmp_path / "workspace"
    workspace.mkdir()
    for setting in ("dependency_install_path", "sdk_install_path", "common_install_path"):
        directory = tmp_path / setting
        directory.mkdir()
        monkeypatch.setattr(settings, setting, str(directory))
    return BubblewrapRunner(workspace)


@pytest.mark.asyncio
async def test_real_pid_and_network_namespaces(runner):
    host_pid_namespace = os.readlink("/proc/self/ns/pid")
    host_net_namespace = os.readlink("/proc/self/ns/net")
    code = """import os
def handler(event):
    return {"pid": os.readlink("/proc/self/ns/pid"),
            "net": os.readlink("/proc/self/ns/net"), "cwd": os.getcwd()}
"""
    result = await runner.execute(execution(runner.workspace_path, code))
    assert result.exit_code == 0, result.stderr
    assert result.return_value["pid"] != host_pid_namespace
    assert result.return_value["net"] != host_net_namespace
    assert result.return_value["cwd"] == "/workspace"


@pytest.mark.asyncio
@pytest.mark.parametrize("byte_size", [131070, 131071, 131072, 131073])
@pytest.mark.parametrize("fill", ["x", "中"])
async def test_real_source_byte_boundaries(runner, byte_size, fill):
    base = 'def handler(event):\n    print("before")\n    return event\n#'
    item = execution(runner.workspace_path, base, event={"value": "中文"})
    wrapper = runner._build_command(item)[0][-1]
    count, remainder = divmod(byte_size - len(wrapper.encode()), len(fill.encode()))
    item.code = base + fill * count + "x" * remainder
    assert len(runner._build_command(item)[0][-1].encode()) == byte_size
    result = await runner.execute(item)
    assert result.exit_code == 0, result.stderr
    assert result.return_value == {"value": "中文"}
    assert "before" in result.stdout


@pytest.mark.asyncio
async def test_real_character_limit_and_large_source_event(runner):
    base = """import subprocess, sys
def handler(event):
    result = subprocess.run([sys.executable, "-c", "print(42)"], capture_output=True, text=True)
    return {"event_length": len(event["code"]), "stdout": result.stdout, "exit": result.returncode}
#"""
    code = base + "中" * (102400 - len(base))
    result = await runner.execute(execution(runner.workspace_path, code, event={"code": code}))
    assert result.exit_code == 0, result.stderr
    assert result.return_value == {"event_length": 102400, "stdout": "42\n", "exit": 0}


@pytest.mark.asyncio
@pytest.mark.parametrize("cancel", [False, True])
async def test_real_timeout_and_cancel_remove_descendants(runner, cancel):
    marker = runner.workspace_path / "descendant.json"
    code = """import json, os, subprocess, sys, time
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
with open("/workspace/descendant.json", "w") as output:
    json.dump({"namespace": os.readlink("/proc/self/ns/pid")}, output)
def handler(event):
    time.sleep(60)
"""
    task = asyncio.create_task(runner.execute(execution(runner.workspace_path, code, timeout=2)))
    for _ in range(150):
        if marker.exists() or task.done():
            break
        await asyncio.sleep(0.01)
    assert marker.exists(), (await task).stderr
    namespace = json.loads(marker.read_text())["namespace"]
    if cancel:
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    else:
        result = await task
        assert result.status.value == "timeout"
    # Namespace IDs identify actual surviving processes without confusing the
    # sandbox's PID numbers with host PIDs.
    survivors = []
    for entry in Path("/proc").iterdir():
        if entry.name.isdigit():
            try:
                if os.readlink(entry / "ns/pid") == namespace:
                    survivors.append(entry.name)
            except (FileNotFoundError, PermissionError):
                pass
    assert survivors == []


@pytest.mark.asyncio
@pytest.mark.parametrize("characters", [102399, 102400])
async def test_real_ptc_bootstrap_with_candidate_sdk(runner, monkeypatch, characters):
    bootstrap_path = os.environ.get("BKN_PTC_BOOTSTRAP_PATH")
    sdk_path = os.environ.get("BKN_CANDIDATE_SDK_PATH")
    if not bootstrap_path or not sdk_path:
        pytest.skip("provide PTC bootstrap and candidate SDK paths for combined validation")
    monkeypatch.setattr(settings, "sdk_install_path", sdk_path)
    # This isolated validation stores the SDK under /tmp, unlike production's
    # /opt. Bind it after the /tmp tmpfs so it remains visible and read-only.
    build_base_args = runner._build_base_args
    monkeypatch.setattr(
        runner,
        "_build_base_args",
        lambda cwd: build_base_args(cwd) + ["--ro-bind", sdk_path, sdk_path],
    )
    base = """import subprocess, sys, os
from sandbox_sdk import _bkn_tools
child = subprocess.run([sys.executable, "-c", "print(42)"], capture_output=True, text=True)
return {"version": _bkn_tools.__toolkit_version__, "stdout": child.stdout,
        "exit": child.returncode, "cwd": os.getcwd(), "source_removed": "_ptc_source" not in event}
#"""
    source = base + "中" * (characters - len(base))
    bootstrap = Path(bootstrap_path).read_text()
    version_assignments = [
        node.value
        for node in ast.parse(bootstrap).body
        if isinstance(node, ast.Assign)
        and any(
            isinstance(target, ast.Name) and target.id == "_EXPECTED_TOOLKIT_VERSION"
            for target in node.targets
        )
    ]
    assert len(version_assignments) == 1
    expected_version = ast.literal_eval(version_assignments[0])
    result = await runner.execute(
        execution(
            runner.workspace_path,
            bootstrap,
            event={"_ptc_source": source, "workspace_scope": "ptc-isolated"},
        )
    )
    assert result.exit_code == 0, result.stderr
    assert result.return_value == {
        "version": expected_version,
        "stdout": "42\n",
        "exit": 0,
        "cwd": "/workspace/ptc-isolated",
        "source_removed": True,
    }


def bwrap_processes():
    result = set()
    for path in Path("/proc").iterdir():
        if path.name.isdigit():
            try:
                if (path / "comm").read_text().strip() == "bwrap":
                    result.add(int(path.name))
            except (FileNotFoundError, PermissionError, ProcessLookupError):
                pass
    return result


@pytest.mark.asyncio
async def test_real_early_cancellation_does_not_leave_new_bwrap_processes(runner):
    before = bwrap_processes()
    for delay in (0.0005, 0.002, 0.005, 0.01, 0.02):
        task = asyncio.create_task(
            runner.execute(
                execution(
                    runner.workspace_path,
                    "import time\ndef handler(event):\n    time.sleep(60)",
                )
            )
        )
        await asyncio.sleep(delay)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    await asyncio.sleep(0.3)
    assert bwrap_processes() - before == set()
