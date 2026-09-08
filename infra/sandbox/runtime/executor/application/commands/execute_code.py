"""
Execute Code Command

Main execution orchestrator use case.
Coordinates domain objects to execute code in isolation.
"""

import asyncio
from pathlib import Path
from typing import Optional
import structlog

from executor.domain.entities import Execution
from executor.domain.value_objects import (
    ExecutionContext,
    ExecutionResult,
    ExecutionStatus,
    ExecutionRequest,
    ResourceLimit,
)
from executor.domain.ports import (
    IIsolationPort,
    IArtifactScannerPort,
    ICallbackPort,
    IHeartbeatPort,
)

logger = structlog.get_logger(__name__)


class ExecuteCodeCommand:
    """
    Command handler for code execution use case.

    Orchestrates the execution flow:
    1. Create execution entity from request
    2. Start heartbeat
    3. Execute code via isolation port
    4. Collect artifacts
    5. Stop heartbeat
    6. Report result to Control Plane
    """

    def __init__(
        self,
        isolation_port: IIsolationPort,
        artifact_scanner_port: IArtifactScannerPort,
        callback_port: ICallbackPort,
        heartbeat_port: IHeartbeatPort,
        workspace_path: Path,
        control_plane_url: str,
    ):
        """
        Initialize the execute code command.

        Args:
            isolation_port: Port for code isolation (Bubblewrap)
            artifact_scanner_port: Port for artifact collection
            callback_port: Port for Control Plane callbacks
            heartbeat_port: Port for heartbeat management
            workspace_path: Path to workspace directory
            control_plane_url: Control Plane base URL
        """
        self._isolation_port = isolation_port
        self._artifact_scanner_port = artifact_scanner_port
        self._callback_port = callback_port
        self._heartbeat_port = heartbeat_port
        self._workspace_path = workspace_path
        self._control_plane_url = control_plane_url
        self._active_executions: set = set()

    def get_active_count(self) -> int:
        """Get the number of active executions."""
        return len(self._active_executions)

    async def execute(self, request: ExecutionRequest) -> ExecutionResult:
        """
        Execute code from request.

        This is the main entry point for code execution.

        Args:
            request: Execution request value object

        Returns:
            ExecutionResult with all execution data
        """
        logger.info(
            "Starting execution",
            execution_id=request.execution_id,
            language=request.language,
            timeout=request.timeout,
        )

        # Create execution context from request
        context = request.to_context(self._workspace_path, self._control_plane_url)

        # Create execution entity
        execution = Execution(
            execution_id=request.execution_id,
            session_id=context.session_id,
            code=request.code,
            language=request.language,
            context=context,
            timeout_seconds=request.timeout,
        )

        # Mark as running
        execution.mark_as_running()

        # Track active execution
        self._active_executions.add(execution.execution_id)

        # Start heartbeat
        await self._heartbeat_port.start_heartbeat(execution_id=execution.execution_id)

        # Artifact scanning is scoped to the execution's working directory. The
        # workspace root is shared by every session on this node, so scanning it
        # walks unrelated directories and reports their files as this execution's
        # output.
        scan_root = self._resolve_scan_root(context)

        # Snapshot before execution; artifacts are the files that appear after it.
        base_snapshot = self._artifact_scanner_port.snapshot(scan_root)

        try:
            # Execute with timeout
            result = await self._execute_with_timeout(
                execution=execution,
                timeout_seconds=request.timeout,
                base_snapshot=base_snapshot,
                scan_root=scan_root,
            )

            # Mark as completed
            execution.mark_as_completed(result)

            # Report result (fire-and-forget, don't block response)
            asyncio.create_task(self._report_result(execution.execution_id, result))
            return result

        except asyncio.TimeoutError:
            logger.warning("Execution timeout", execution_id=execution.execution_id)
            execution.mark_as_timeout()

            timeout_result = ExecutionResult(
                status=ExecutionStatus.TIMEOUT,
                stdout="",
                stderr=f"Execution timeout after {request.timeout}s",
                exit_code=-1,
                execution_time_ms=request.timeout * 1000,
            )

            # Report timeout (fire-and-forget)
            asyncio.create_task(self._report_result(execution.execution_id, timeout_result))

            return timeout_result

        except Exception as e:
            logger.error(
                "Execution failed",
                execution_id=execution.execution_id,
                error=str(e),
                exc_info=True,
            )
            execution.mark_as_failed(str(e))

            error_result = ExecutionResult(
                status=ExecutionStatus.ERROR,
                stdout="",
                stderr=str(e),
                exit_code=-1,
                execution_time_ms=0,
                error=str(e),
            )

            # Report error (fire-and-forget)
            asyncio.create_task(self._report_result(execution.execution_id, error_result))

            return error_result

        finally:
            # Stop heartbeat
            await self._heartbeat_port.stop_heartbeat(execution_id=execution.execution_id)
            # Remove from active executions
            self._active_executions.discard(execution.execution_id)

    def _resolve_scan_root(self, context: ExecutionContext) -> Path:
        """
        Directory used for both the pre-execution snapshot and artifact collection.

        Creates the working directory when it is missing so that the first
        execution in a new one succeeds. Falls back to the workspace root when the
        working directory cannot be resolved, which keeps callers that send no
        working directory behaving as before.

        Args:
            context: Execution context

        Returns:
            Absolute path to scan for artifacts
        """
        try:
            return context.resolve_working_directory_path(create=True)
        except (ValueError, OSError) as exc:
            logger.warning(
                "Failed to resolve working directory, scanning workspace root",
                working_directory=context.working_directory,
                error=str(exc),
            )
            return self._workspace_path

    async def _execute_with_timeout(
        self,
        execution: Execution,
        timeout_seconds: int,
        base_snapshot: set,
        scan_root: Path,
    ) -> ExecutionResult:
        """
        Execute code with timeout enforcement.

        Args:
            execution: Execution entity
            timeout_seconds: Timeout in seconds
            base_snapshot: Pre-execution file snapshot
            scan_root: Directory scanned for artifacts

        Returns:
            ExecutionResult

        Raises:
            asyncio.TimeoutError: If execution exceeds timeout
        """
        return await asyncio.wait_for(
            self._execute_internal(execution, base_snapshot, scan_root),
            timeout=timeout_seconds,
        )

    async def _execute_internal(
        self,
        execution: Execution,
        base_snapshot: set,
        scan_root: Path,
    ) -> ExecutionResult:
        """
        Internal execution logic.

        Args:
            execution: Execution entity
            base_snapshot: Pre-execution file snapshot
            scan_root: Directory scanned for artifacts

        Returns:
            ExecutionResult
        """
        # Execute via isolation port
        result = await self._isolation_port.execute(execution)

        # Collect artifacts using artifact scanner port
        from executor.domain.value_objects import Artifact, ArtifactType

        artifacts_data = self._artifact_scanner_port.collect_artifacts(
            workspace_path=scan_root,
            include_hidden=False,
            include_temp=False,
        )

        # Only files that appeared during this execution are artifacts. Files that
        # were already present belong to earlier executions sharing the directory.
        artifacts = []
        for artifact_data in artifacts_data:
            if artifact_data.path in base_snapshot:
                continue
            artifacts.append(
                Artifact(
                    path=artifact_data.path,
                    size=artifact_data.size,
                    mime_type=artifact_data.mime_type,
                    type=artifact_data.type,
                    created_at=artifact_data.created_at,
                    checksum=artifact_data.checksum,
                )
            )

        result.artifacts = artifacts
        return result

    async def _report_result(
        self,
        execution_id: str,
        result: ExecutionResult,
    ) -> None:
        """
        Report execution result to Control Plane.

        Args:
            execution_id: Unique execution identifier
            result: Execution result
        """
        try:
            success = await self._callback_port.report_result(execution_id, result)
            if success:
                logger.info("Result reported successfully", execution_id=execution_id)
            else:
                logger.warning("Failed to report result", execution_id=execution_id)
        except Exception as e:
            logger.error(
                "Error reporting result",
                execution_id=execution_id,
                error=str(e),
            )
