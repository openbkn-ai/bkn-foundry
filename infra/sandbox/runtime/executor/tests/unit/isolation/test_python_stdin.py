"""Regression coverage for source exceeding Linux's single-argument byte limit."""

import asyncio
import json
import os

import pytest

from executor.domain.entities import Execution
from executor.domain.value_objects import ExecutionContext, ExecutionStatus
from executor.infrastructure.isolation.bwrap import BubblewrapRunner
from executor.infrastructure.isolation.python_stdin import PYTHON_STDIN_BOOTSTRAP
from executor.infrastructure.isolation.subprocess import SubprocessRunner


def execution(tmp_path, code, event=None, env_vars=None, timeout=10):
    context = ExecutionContext(
        workspace_path=tmp_path,
        session_id="session",
        execution_id="execution",
        control_plane_url="http://localhost:8000",
        env_vars=env_vars or {},
        event=event or {},
    )
    return Execution(
        execution_id="execution",
        session_id="session",
        code=code,
        language="python",
        context=context,
        timeout_seconds=timeout,
    )


@pytest.fixture(params=[SubprocessRunner, BubblewrapRunner], ids=["subprocess", "bwrap"])
def runner(request, tmp_path, monkeypatch):
    instance = request.param(tmp_path)
    if request.param is BubblewrapRunner:
        # Exercise the actual child Python locally, substituting only bwrap's
        # namespace boundary. Real namespace cleanup still requires Linux IT.
        original_spawn = asyncio.create_subprocess_exec

        async def spawn(*cmd, **kwargs):
            assert max(len(arg.encode("utf-8")) for arg in cmd) < 131072
            assert "EVENT_JSON" not in kwargs["env"]
            assert cmd[-1] == PYTHON_STDIN_BOOTSTRAP
            return await original_spawn(*cmd[cmd.index("--") + 1 :], **kwargs)

        monkeypatch.setattr(
            "executor.infrastructure.isolation.bwrap.asyncio.create_subprocess_exec", spawn
        )
    return instance


@pytest.mark.asyncio
@pytest.mark.parametrize("target_bytes", [131070, 131071, 131072, 131073])
@pytest.mark.parametrize("fill", ["x", "中"])
async def test_source_byte_boundaries(runner, tmp_path, target_bytes, fill):
    base = 'def handler(event):\n    print("before")\n    return event\n#'
    initial = execution(tmp_path, base)
    wrapped = runner._build_command(initial)[0][-1]
    count, remainder = divmod(target_bytes - len(wrapped.encode()), len(fill.encode()))
    code = base + fill * count + "x" * remainder
    item = execution(tmp_path, code)
    assert len(runner._build_command(item)[0][-1].encode()) == target_bytes
    result = await runner.execute(item)
    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == {}
    assert "before" in result.stdout
    assert result.stderr == ""


@pytest.mark.asyncio
async def test_large_event_and_command_semantics(runner, tmp_path):
    event = {"text": "中文" * 30000}
    (tmp_path / "local_module.py").write_text('VALUE = "local"\n')
    code = """import sys, local_module

def handler(event):
    return {"event": event, "path": sys.path[0], "file": "__file__" in globals(),
            "name": __name__, "local": local_module.VALUE}
"""
    result = await runner.execute(execution(tmp_path, code, event=event))
    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == {
        "event": event,
        "path": "",
        "file": False,
        "name": "__main__",
        "local": "local",
    }


@pytest.mark.asyncio
async def test_bwrap_event_environment_override_is_preserved(runner, tmp_path):
    if not isinstance(runner, BubblewrapRunner):
        return
    event_json = json.dumps({"text": "中" * 50000})
    result = await runner.execute(
        execution(
            tmp_path,
            'import os\ndef handler(event):\n    return [event, os.environ["EVENT_JSON"]]',
            event={"ignored": True},
            env_vars={"EVENT_JSON": event_json},
        )
    )
    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == [json.loads(event_json), event_json]


@pytest.mark.asyncio
async def test_concurrent_sources_do_not_mix(runner, tmp_path):
    async def run(value):
        return await runner.execute(
            execution(
                tmp_path,
                f"def handler(event):\n    return [{value}, event]\n#" + "中" * 45000,
                event={"id": value},
            )
        )

    results = await asyncio.gather(run(1), run(2))
    assert [result.return_value for result in results] == [[1, {"id": 1}], [2, {"id": 2}]]
    assert not list(tmp_path.iterdir())


@pytest.mark.asyncio
@pytest.mark.parametrize("cancel", [False, True], ids=["timeout", "cancel"])
async def test_bwrap_cleans_process_group_after_timeout_or_cancel(runner, tmp_path, cancel):
    if not isinstance(runner, BubblewrapRunner):
        return
    pid_file = tmp_path / "child.pid"
    code = f"""import subprocess, sys, time
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
with open({str(pid_file)!r}, "w") as output:
    output.write(str(child.pid))
def handler(event):
    time.sleep(60)
"""
    task = asyncio.create_task(runner.execute(execution(tmp_path, code, timeout=1)))
    if cancel:
        for _ in range(200):
            if pid_file.exists():
                break
            await asyncio.sleep(0.01)
        assert pid_file.exists()
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
    else:
        result = await task
        assert result.status == ExecutionStatus.TIMEOUT
    await asyncio.sleep(0.2)
    assert pid_file.exists()
    with pytest.raises(OSError):
        os.kill(int(pid_file.read_text()), 0)


@pytest.mark.asyncio
@pytest.mark.parametrize("characters", [102399, 102400])
async def test_product_character_limit_scripts(runner, tmp_path, characters):
    base = 'def handler(event):\n    return "ok"\n#'
    code = base + "中" * (characters - len(base))
    assert len(code) == characters
    result = await runner.execute(execution(tmp_path, code))
    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == "ok"


@pytest.mark.asyncio
async def test_script_failure_preserves_stderr_and_exit(runner, tmp_path):
    result = await runner.execute(
        execution(
            tmp_path,
            'import sys\nprint("failure", file=sys.stderr)\nraise SystemExit(7)',
        )
    )
    assert result.status == ExecutionStatus.FAILED
    assert result.exit_code == 7
    assert result.stderr == "failure\n"
    assert not list(tmp_path.iterdir())


@pytest.mark.asyncio
async def test_source_in_event_does_not_break_user_subprocess(runner, tmp_path):
    source = "#" + "中" * 102399
    code = """import subprocess, sys
def handler(event):
    child = subprocess.run([sys.executable, "-c", "print(42)"], capture_output=True, text=True)
    return {"length": len(event["code"]), "stdout": child.stdout, "exit": child.returncode}
"""
    result = await runner.execute(execution(tmp_path, code, event={"code": source}))
    assert result.status == ExecutionStatus.COMPLETED
    assert result.return_value == {"length": 102400, "stdout": "42\n", "exit": 0}


@pytest.mark.asyncio
async def test_bwrap_timeout_cleans_children_after_parent_exits(runner, tmp_path):
    if not isinstance(runner, BubblewrapRunner):
        return
    pid_file = tmp_path / "orphan.pid"
    code = f"""import subprocess, sys
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
with open({str(pid_file)!r}, "w") as output:
    output.write(str(child.pid))
def handler(event):
    return None
"""
    result = await runner.execute(execution(tmp_path, code, timeout=1))
    assert result.status == ExecutionStatus.TIMEOUT
    await asyncio.sleep(0.2)
    with pytest.raises(OSError):
        os.kill(int(pid_file.read_text()), 0)
