"""
Unit tests for SubprocessRunner timeout handling.

Covers the three behaviours that used to be wrong: the caller's timeout was
ignored in favour of a hardcoded 30s, a timed-out process was left running, and
the reported duration was a constant.
"""

import asyncio
import os
import signal
import time

import pytest

from executor.domain.entities import Execution
from executor.domain.value_objects import ExecutionContext, ExecutionStatus
from executor.infrastructure.isolation.subprocess import SubprocessRunner


def _execution(code: str, timeout_seconds=None, tmp_path=None) -> Execution:
    context = ExecutionContext(
        workspace_path=tmp_path,
        session_id="sess-test",
        execution_id="exec-test",
        control_plane_url="http://localhost:8000",
        env_vars={},
        event={},
    )
    return Execution(
        execution_id="exec-test",
        session_id="sess-test",
        code=code,
        language="python",
        context=context,
        timeout_seconds=timeout_seconds,
    )


@pytest.mark.asyncio
async def test_caller_timeout_is_honoured(tmp_path):
    """A 1s timeout must fire in about a second, not after the old 30s default."""
    runner = SubprocessRunner(tmp_path)
    execution = _execution(
        "def handler(event):\n    import time; time.sleep(30)\n    return {}",
        timeout_seconds=1,
        tmp_path=tmp_path,
    )

    start = time.perf_counter()
    result = await runner.execute(execution)
    elapsed = time.perf_counter() - start

    assert result.status == ExecutionStatus.TIMEOUT
    assert result.exit_code == 124
    assert elapsed < 10, f"timeout took {elapsed}s, caller timeout was ignored"


@pytest.mark.asyncio
async def test_reported_duration_is_measured(tmp_path):
    """execution_time_ms reflects real elapsed time rather than a constant."""
    runner = SubprocessRunner(tmp_path)
    execution = _execution(
        "def handler(event):\n    import time; time.sleep(30)\n    return {}",
        timeout_seconds=1,
        tmp_path=tmp_path,
    )

    result = await runner.execute(execution)

    assert result.execution_time_ms != 30000
    assert 500 < result.execution_time_ms < 10000


@pytest.mark.asyncio
async def test_timed_out_process_group_is_killed(tmp_path):
    """The child and anything it spawned are gone once the timeout returns."""
    marker = tmp_path / "child.pid"
    # Parent spawns a grandchild that would outlive a plain process.kill()
    code = f'''
import subprocess, sys, time
def handler(event):
    child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
    open({str(marker)!r}, "w").write(str(child.pid))
    time.sleep(60)
    return {{}}
'''
    runner = SubprocessRunner(tmp_path)
    execution = _execution(code, timeout_seconds=2, tmp_path=tmp_path)

    result = await runner.execute(execution)
    assert result.status == ExecutionStatus.TIMEOUT

    # Give the signal a moment to propagate through the group
    await asyncio.sleep(0.5)
    assert marker.exists(), "grandchild was never started; test is not exercising the kill"
    grandchild_pid = int(marker.read_text())

    with pytest.raises(OSError):
        # Signal 0 only probes for existence
        os.kill(grandchild_pid, 0)


@pytest.mark.asyncio
async def test_successful_run_is_unaffected(tmp_path):
    """The happy path still returns the value and a real duration."""
    runner = SubprocessRunner(tmp_path)
    execution = _execution(
        'def handler(event):\n    return {"ok": True}',
        timeout_seconds=10,
        tmp_path=tmp_path,
    )

    result = await runner.execute(execution)

    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == {"ok": True}
    assert result.execution_time_ms > 0


@pytest.mark.asyncio
async def test_outer_cancellation_still_kills_the_process(tmp_path):
    """
    The real call chain wraps execute() in its own wait_for with the same
    timeout, and starts counting first — so it cancels us before our own
    timeout fires. Cleanup has to survive that, not just TimeoutError.
    """
    marker = tmp_path / "child.pid"
    code = f'''
import subprocess, sys, time
def handler(event):
    child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
    open({str(marker)!r}, "w").write(str(child.pid))
    time.sleep(60)
    return {{}}
'''
    runner = SubprocessRunner(tmp_path)
    execution = _execution(code, timeout_seconds=2, tmp_path=tmp_path)

    # Same shape as ExecuteCodeCommand._execute_with_timeout
    outer_timed_out = False
    try:
        await asyncio.wait_for(runner.execute(execution), timeout=2)
    except asyncio.TimeoutError:
        outer_timed_out = True

    assert outer_timed_out, "outer layer should be the one that fires"

    await asyncio.sleep(0.5)
    assert marker.exists(), "grandchild was never started; test is not exercising the kill"
    grandchild_pid = int(marker.read_text())

    with pytest.raises(OSError):
        os.kill(grandchild_pid, 0)
