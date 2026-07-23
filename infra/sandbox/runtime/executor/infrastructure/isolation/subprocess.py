"""
Simple subprocess isolation runner.

This is a fallback isolation mechanism for development environments
where Bubblewrap is not available (e.g., macOS Docker containers).

WARNING: This provides NO security isolation and should ONLY be used
for development purposes.
"""
import asyncio
import json
import logging
import os
import signal
import time
from pathlib import Path
from typing import List, Tuple

from executor.domain.entities import Execution
from executor.domain.value_objects import ExecutionResult, ExecutionStatus, ExecutionMetrics
from executor.infrastructure.config import settings
from executor.infrastructure.isolation.code_wrapper import defines_handler, normalize_shell_code, uses_tool_decorator

# 超时后先发 SIGTERM，等这么久仍未退出就 SIGKILL
_TERMINATE_GRACE_SECONDS = 3

logger = logging.getLogger(__name__)

EXECUTION_PATH = "/usr/local/go/bin:/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin"


class SubprocessRunner:
    """
    Executes code using subprocess without isolation.

    WARNING: This is a DEVELOPMENT-ONLY fallback that provides NO
    security isolation. Never use this in production.
    """

    def __init__(self, workspace_path: Path):
        """
        Initialize the subprocess runner.

        Args:
            workspace_path: Path to the workspace directory (can be S3 path)
        """
        # Store original workspace path for reference
        self.original_workspace_path = workspace_path
        workspace_str = str(workspace_path)

        # If workspace is S3 path, use local /workspace directory for execution
        # S3 paths are handled by the artifact storage layer
        if workspace_str.startswith("s3:/") or workspace_str.startswith("s3://"):
            logger.warning(f"Workspace is S3 path ({workspace_str}), using local /workspace for execution")
            self.workspace_path = Path("/workspace")
        else:
            self.workspace_path = workspace_path

        logger.warning(f"SubprocessRunner initialized - NO SECURITY ISOLATION, workspace_path={self.workspace_path}")

    async def execute(self, execution: Execution) -> ExecutionResult:
        """
        Execute code without isolation.

        Args:
            execution: Execution entity with code and context

        Returns:
            ExecutionResult with stdout, stderr, exit code, timing, return_value, and metrics
        """
        start_time = time.perf_counter()
        start_cpu = time.process_time()
        logger.info(f"Executing code without isolation (DEVELOPMENT MODE), execution_id={execution.execution_id}, language={execution.language}")

        process = None

        try:
            # Build language-specific command and environment
            cmd, env_args = self._build_command(execution)
            cwd_path = execution.context.resolve_working_directory_path()

            # start_new_session puts the child in its own process group, so a
            # timeout can take down everything it spawned rather than just the
            # direct child.
            process = await asyncio.create_subprocess_exec(
                *cmd,
                cwd=str(cwd_path),
                env=env_args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                start_new_session=True,
            )

            timeout_seconds = execution.timeout_seconds or settings.default_timeout
            stdout, stderr = await asyncio.wait_for(
                process.communicate(),
                timeout=timeout_seconds,
            )

            duration = time.perf_counter() - start_time
            cpu_time = time.process_time() - start_cpu

            stdout_str = stdout.decode("utf-8", errors="replace")
            stderr_str = stderr.decode("utf-8", errors="replace")

            # Parse return value from stdout (for Lambda handlers)
            return_value = None
            language = execution.language.lower()
            if language in ("python", "python3") and process.returncode == 0:
                try:
                    # Lambda handler writes return value as JSON to stdout
                    # But there might be print() statements before the return value
                    # Strategy: extract the last valid JSON as return_value, keep everything else as stdout
                    lines = stdout_str.strip().split('\n')

                    # Try to find the last valid JSON line as the return value
                    for i in range(len(lines) - 1, -1, -1):
                        line = lines[i].strip()
                        if line:  # Non-empty line
                            try:
                                return_value = json.loads(line)
                                # Found valid JSON - remove this line from stdout
                                stdout_str = '\n'.join(lines[:i])
                                if stdout_str:
                                    # Ensure stdout ends with a single newline if there were previous lines
                                    if not stdout_str.endswith('\n'):
                                        stdout_str += '\n'
                                break
                            except json.JSONDecodeError:
                                # Not valid JSON, continue searching
                                continue
                    else:
                        # No valid JSON found, keep the original stdout
                        pass
                except Exception as e:
                    # If parsing fails, keep the original stdout
                    logger.debug(f"Failed to parse return value from stdout: {e}")
                    pass

            logger.info(f"Execution completed, execution_id={execution.execution_id}, exit_code={process.returncode}, duration_ms={duration * 1000}")

            return ExecutionResult(
                status=ExecutionStatus.COMPLETED if process.returncode == 0 else ExecutionStatus.FAILED,
                stdout=stdout_str,
                stderr=stderr_str,
                exit_code=process.returncode,
                execution_time_ms=duration * 1000,
                return_value=return_value,
                metrics=ExecutionMetrics(
                    duration_ms=duration * 1000,
                    cpu_time_ms=cpu_time * 1000,
                    peak_memory_mb=None,
                    io_read_bytes=None,
                    io_write_bytes=None,
                ),
            )

        except asyncio.TimeoutError:
            # Returning without killing leaves the process — and anything it
            # spawned — burning CPU inside the container while the session is
            # still reported healthy and keeps taking work.
            await self._terminate_process_group(process, execution.execution_id)
            duration_ms = (time.perf_counter() - start_time) * 1000
            logger.warning(
                f"Execution timed out, execution_id={execution.execution_id}, "
                f"duration_ms={duration_ms}"
            )
            return ExecutionResult(
                status=ExecutionStatus.TIMEOUT,
                stdout="",
                stderr="Execution timed out",
                exit_code=124,
                execution_time_ms=duration_ms,
                return_value=None,
                metrics=None,
            )
        except Exception as e:
            logger.error(f"Execution failed, execution_id={execution.execution_id}, error={e}")
            return ExecutionResult(
                status=ExecutionStatus.ERROR,
                stdout="",
                stderr=str(e),
                exit_code=-1,
                execution_time_ms=0,
                return_value=None,
                metrics=None,
            )
        finally:
            # The caller wraps this in its own wait_for with the same timeout and
            # starts counting earlier, so in practice it fires first and cancels
            # us — that surfaces as CancelledError, which the except clauses above
            # do not catch. Without this the process group would survive.
            #
            # Cancellation also makes awaiting unreliable here, so the last resort
            # is a synchronous SIGKILL rather than the graceful sequence.
            self._kill_process_group_now(process, execution.execution_id)

    def _kill_process_group_now(self, process, execution_id: str) -> None:
        """
        Last-resort synchronous kill, safe to call while being cancelled.

        No-op when the process already exited, so the success path pays nothing.
        """
        if process is None or process.returncode is not None:
            return
        try:
            os.killpg(os.getpgid(process.pid), signal.SIGKILL)
            logger.warning(
                f"Killed surviving process group, execution_id={execution_id}, pid={process.pid}"
            )
        except (ProcessLookupError, PermissionError):
            pass
        except Exception as e:
            logger.error(
                f"Failed to kill process group, execution_id={execution_id}, error={e}"
            )

    async def _terminate_process_group(self, process, execution_id: str) -> None:
        """
        Kill the timed-out process and everything it spawned.

        The child was started with start_new_session=True, so it leads its own
        process group and killpg reaches grandchildren too. SIGTERM first to let
        the code clean up, SIGKILL if it does not go away.
        """
        if process is None or process.returncode is not None:
            return
        try:
            pgid = os.getpgid(process.pid)
        except (ProcessLookupError, PermissionError):
            pgid = None

        try:
            if pgid is not None:
                os.killpg(pgid, signal.SIGTERM)
            else:
                process.terminate()
            try:
                await asyncio.wait_for(process.wait(), timeout=_TERMINATE_GRACE_SECONDS)
                return
            except asyncio.TimeoutError:
                pass

            if pgid is not None:
                os.killpg(pgid, signal.SIGKILL)
            else:
                process.kill()
            await process.wait()
        except ProcessLookupError:
            # Already gone between the check and the signal.
            pass
        except Exception as e:
            logger.error(
                f"Failed to terminate process group, execution_id={execution_id}, error={e}"
            )

    def _build_command(self, execution: Execution) -> Tuple[List[str], dict]:
        """
        Build language-specific command and environment.

        Args:
            execution: Execution entity with code and context

        Returns:
            Tuple of (command_list, environment_dict)
        """
        import os
        language = execution.language.lower()
        code = execution.code

        # Build environment variables - inherit from current process
        env_args = os.environ.copy()
        # Override specific variables
        env_args.update({
            "PATH": EXECUTION_PATH,
            "HOME": str(self.workspace_path),
            "USER": "sandbox",
            "WORKSPACE_PATH": str(self.workspace_path),
            "PYTHONPATH": self._build_pythonpath(env_args.get("PYTHONPATH")),
        })

        # Add user-provided environment variables
        if execution.context.env_vars:
            env_args.update(execution.context.env_vars)

        # Get event data as JSON
        event_json = json.dumps(execution.context.event or {})

        # Language-specific commands
        if language in ("python", "python3"):
            # Two entry styles are supported:
            #   @tool     - sandbox_sdk unpacks the event into named parameters
            #   handler   - AWS Lambda style, the original contract
            # Detection is AST-based so @tool in a comment or string is not a
            # false positive, and unparsable code falls back to handler.
            # handler wins when both are present, see code_wrapper for why
            if defines_handler(code) or not uses_tool_decorator(code):
                preamble = ""
                invoke = "handler(event_data)"
            else:
                preamble = "import sandbox_sdk"
                invoke = "sandbox_sdk.dispatch(event_data)"
            wrapped_code = f'''
import json
import sys
{preamble}

# User's code
{code}

# Invoke the user function
if __name__ == "__main__":
    event_data = json.loads({json.dumps(event_json)})
    result = {invoke}
    print(json.dumps(result))
'''
            cmd = ["python3", "-c", wrapped_code]
        elif language == "javascript":
            # For JavaScript handlers (Node.js)
            wrapped_code = f'''
// User's code
{code}

// Execute the Lambda handler
const eventData = {json.dumps(event_json)};
const result = handler(eventData);
console.log(JSON.stringify(result));
'''
            cmd = ["node", "-e", wrapped_code]
        elif language == "shell":
            code = normalize_shell_code(code, execution.context.resolve_working_directory_path())
            cmd = ["sh", "-c", code]
        else:
            raise ValueError(f"Unsupported language: {language}")

        return cmd, env_args

    def _build_pythonpath(self, existing_pythonpath: str | None) -> str:
        dependency_path = settings.dependency_install_path
        sdk_path = settings.sdk_install_path
        parts = [sdk_path, dependency_path]
        if existing_pythonpath:
            parts.append(existing_pythonpath)
        return ":".join(parts)
