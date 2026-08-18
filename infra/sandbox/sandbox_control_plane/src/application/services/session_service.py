"""
Session application service

Orchestrates the session use cases.
"""

from typing import Callable, List, Optional
from datetime import datetime, timedelta
import uuid

from src.domain.entities.session import InstalledDependency, Session
from src.domain.entities.execution import Execution
from src.domain.entities.template import Template
from src.domain.value_objects.resource_limit import ResourceLimit
from src.domain.value_objects.execution_status import SessionStatus, ExecutionStatus
from src.domain.value_objects.execution_request import ExecutionRequest
from src.domain.repositories.session_repository import ISessionRepository
from src.domain.repositories.execution_repository import IExecutionRepository
from src.domain.repositories.template_repository import ITemplateRepository
from src.domain.services.scheduler import IScheduler, ScheduleRequest, RuntimeNode
from src.domain.services.storage import IStorageService
from src.application.commands.create_session import CreateSessionCommand
from src.application.commands.install_session_dependencies import (
    InstallSessionDependenciesCommand,
)
from src.infrastructure.config.settings import get_settings
from src.application.commands.execute_code import ExecuteCodeCommand
from src.application.queries.get_session import GetSessionQuery
from src.application.queries.get_execution import GetExecutionQuery
from src.application.dtos.session_dto import SessionDTO
from src.application.dtos.execution_dto import ExecutionDTO
from src.shared.errors.domain import NotFoundError, ValidationError, ConflictError
from src.infrastructure.executors import ExecutorClient
from src.infrastructure.executors.errors import (
    ExecutorConnectionError,
    ExecutorResponseError,
    ExecutorTimeoutError,
    ExecutorUnavailableError,
    ExecutorValidationError,
)
from src.infrastructure.logging import get_logger
from src.shared.i18n import message
from src.shared.utils.dependencies import (
    DEFAULT_PYTHON_PACKAGE_INDEX_URL,
    normalize_python_package_index_url,
)

logger = get_logger(__name__)


class SessionService:
    """
    Session application service

    Orchestrates creating, executing, and terminating a session.
    """

    def __init__(
        self,
        session_repo: ISessionRepository,
        execution_repo: IExecutionRepository,
        template_repo: ITemplateRepository,
        scheduler: IScheduler,
        storage_service: Optional[IStorageService] = None,
        executor_client: Optional[ExecutorClient] = None,
        initial_dependency_sync_scheduler: Optional[Callable[[str, int], None]] = None,
    ):
        self._session_repo = session_repo
        self._execution_repo = execution_repo
        self._template_repo = template_repo
        self._scheduler = scheduler
        self._storage_service = storage_service
        self._executor_client = executor_client or ExecutorClient()
        self._initial_dependency_sync_scheduler = initial_dependency_sync_scheduler

    async def create_session(self, command: CreateSessionCommand) -> SessionDTO:
        """
        Create-session use case

        Steps:
        1. Verify the template exists
        2. Generate the session id
        3. Ask the scheduler to pick a runtime node
        4. Build the session entity
        5. Persist it
        6. Create the Docker container
        7. Move the session to running
        """
        if not command.template_id:
            settings = get_settings()
            command.template_id = settings.default_template_id

        logger.info(
            "Creating session",
            template_id=command.template_id,
            has_dependencies=len(command.dependencies or []) > 0,
        )

        # 1. Verify the template
        template = await self._validate_template(command.template_id)

        # 2. Resolve the session id, given or generated
        if command.id:
            # A given id: check for a collision
            session_id = command.id
            existing_session = await self._session_repo.find_by_id(session_id)
            if existing_session:
                logger.warning(
                    "Session ID already exists",
                    session_id=session_id,
                    existing_status=existing_session.status.value,
                )
                raise ConflictError(f"Session ID already exists: {session_id}")
            logger.debug("Using manually specified session ID", session_id=session_id)
        else:
            # Generate the session id
            session_id = self._generate_session_id()
            logger.debug("Generated session ID", session_id=session_id)

        # 3. Call the scheduler
        runtime_node = await self._schedule_session(command, session_id)

        # 4. Build the session entity
        session = self._create_session_entity(
            session_id=session_id,
            command=command,
            template=template,
            runtime_node=runtime_node,
        )

        # 5. Persist it
        await self._session_repo.save(session)
        logger.debug("Session saved to repository", session_id=session_id)

        # 6. Create the container
        container_id = await self._create_container_for_session(
            session=session,
            template=template,
            command=command,
            runtime_node=runtime_node,
        )

        logger.info(
            "Session created successfully",
            session_id=session_id,
            container_id=container_id,
            status=session.status.value,
        )

        if dependencies := (command.dependencies or []):
            session.mark_dependency_installing()
            await self._session_repo.save(session)
            self._schedule_initial_dependency_sync(
                session_id=session.id,
                install_timeout=command.install_timeout,
                dependency_count=len(dependencies),
            )

        return SessionDTO.from_entity(session)

    async def _validate_template(self, template_id: str) -> Template:
        """Verify the template exists"""
        from src.domain.entities.template import Template

        template = await self._template_repo.find_by_id(template_id)
        if not template:
            logger.error("Template not found", template_id=template_id)
            raise NotFoundError(message("Sandbox.Template.NotFound", template_id=template_id))

        logger.debug("Template validated", template_id=template.id, image=template.image)
        return template

    async def _schedule_session(
        self, command: CreateSessionCommand, session_id: str
    ) -> RuntimeNode:
        """Schedule the session onto a runtime node"""
        schedule_request = ScheduleRequest(
            template_id=command.template_id,
            resource_limit=command.resource_limit or ResourceLimit.default(),
            session_id=session_id,
        )
        runtime_node = await self._scheduler.schedule(schedule_request)

        logger.info(
            "Runtime node selected",
            session_id=session_id,
            runtime_node=runtime_node.id,
            node_type=runtime_node.type,
        )
        return runtime_node

    def _create_session_entity(
        self,
        session_id: str,
        command: CreateSessionCommand,
        template,
        runtime_node: RuntimeNode,
    ) -> Session:
        """Build the session entity"""
        from src.domain.entities.template import Template

        runtime_type = self._infer_runtime_type(template.image)
        resource_limit = command.resource_limit or ResourceLimit.default()
        settings = get_settings()
        workspace_path = f"s3://{settings.s3_bucket}/sessions/{session_id}"
        dependencies = command.dependencies or []

        return Session(
            id=session_id,
            template_id=command.template_id,
            status=SessionStatus.CREATING,
            resource_limit=resource_limit,
            workspace_path=workspace_path,
            runtime_type=runtime_type,
            runtime_node=runtime_node.id,
            env_vars=command.env_vars or {},
            timeout=command.timeout,
            python_package_index_url=normalize_python_package_index_url(
                command.python_package_index_url
            ),
            requested_dependencies=dependencies,
            dependency_install_status="pending" if dependencies else "completed",
        )

    async def _create_container_for_session(
        self,
        session: Session,
        template,
        command: CreateSessionCommand,
        runtime_node: RuntimeNode,
    ) -> Optional[str]:
        """Create the container for a session"""
        from src.domain.entities.template import Template

        container_id = None
        dependencies: list[str] = []

        try:
            if hasattr(self._scheduler, "create_container_for_session"):
                logger.info(
                    "Creating container for session",
                    session_id=session.id,
                    image=template.image,
                    dependencies_count=len(dependencies),
                    dependencies=dependencies,
                    runtime_node_id=runtime_node.id,
                    runtime_node_type=runtime_node.type,
                )

                container_id = await self._scheduler.create_container_for_session(
                    session_id=session.id,
                    template_id=command.template_id,
                    image=template.image,
                    resource_limit=session.resource_limit,
                    env_vars=session.env_vars,
                    workspace_path=session.workspace_path,
                    node_id=runtime_node.id,
                    dependencies=dependencies,
                )

                session.container_id = container_id
                await self._session_repo.save(session)

                logger.info(
                    "Container created successfully, session saved",
                    session_id=session.id,
                    container_id=container_id,
                    runtime_node=runtime_node.id,
                    dependencies_count=len(dependencies),
                    session_status=session.status.value,
                )
            else:
                logger.warning(
                    "Scheduler does not support create_container_for_session",
                    scheduler_type=type(self._scheduler).__name__,
                )
        except Exception as e:
            logger.exception(
                "Exception during container creation",
                session_id=session.id,
                error_type=type(e).__name__,
                error=str(e),
            )
            await self._handle_container_creation_failure(
                session=session,
                container_id=container_id,
                error=e,
            )

        return container_id

    async def _handle_container_creation_failure(
        self,
        session: Session,
        container_id: Optional[str],
        error: Exception,
    ) -> None:
        """Handle a failed container creation"""
        logger.exception(
            "Container creation failed, starting cleanup",
            session_id=session.id,
            container_id=container_id,
            error_type=type(error).__name__,
            error=str(error),
        )

        # Clean up the container that was created
        if container_id and hasattr(self._scheduler, "destroy_container"):
            try:
                logger.info("Attempting to clean up failed container", container_id=container_id)
                await self._scheduler.destroy_container(container_id)
                logger.debug("Cleaned up failed container", container_id=container_id)
            except Exception as cleanup_error:
                logger.warning(
                    "Failed to cleanup container",
                    container_id=container_id,
                    cleanup_error=str(cleanup_error),
                )

        # Mark the session failed
        session.status = SessionStatus.FAILED
        if session.has_dependencies():
            session.set_dependencies_failed(str(error))
        await self._session_repo.save(session)

        logger.error(
            "Session creation failed",
            session_id=session.id,
            final_status=session.status.value,
            container_id=container_id,
        )
        raise ValidationError(message("Sandbox.Session.ContainerCreateFailed", error=error))

    async def get_session(self, query: GetSessionQuery) -> SessionDTO:
        """Get-session use case"""
        session = await self._session_repo.find_by_id(query.session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=query.session_id))

        return SessionDTO.from_entity(session)

    async def install_session_dependencies(
        self,
        command: InstallSessionDependenciesCommand,
    ) -> SessionDTO:
        """Install session dependencies incrementally."""
        session = await self._session_repo.find_by_id(command.session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=command.session_id))

        if session.dependency_install_status == "installing":
            raise ConflictError(
                f"Dependency installation already in progress for session: {session.id}"
            )

        session.merge_requested_dependencies(
            command.python_package_index_url,
            command.dependencies,
        )
        return await self._sync_session_dependencies(
            session,
            sync_mode="merge",
            executor_timeout=command.install_timeout,
        )

    async def sync_session_dependencies_for_session(
        self,
        session_id: str,
        sync_mode: str = "replace",
    ) -> SessionDTO:
        """Sync the dependency configuration of one session."""
        session = await self._session_repo.find_by_id(session_id)
        if not session:
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))
        return await self._sync_session_dependencies(session, sync_mode=sync_mode)

    async def list_sessions(
        self,
        status: Optional[str] = None,
        template_id: Optional[str] = None,
        limit: int = 50,
        offset: int = 0,
    ) -> dict:
        """
        List-sessions use case

        Args:
            status: filter by session status, optional
            template_id: filter by template id, optional
            limit: how many to return, 1-200, default 50
            offset: offset, for paging

        Returns:
            A dict holding items, total, limit, offset, and has_more
        """
        # Validate the limit range
        limit = max(1, min(limit, 200))
        offset = max(0, offset)

        # Read the session list
        sessions = await self._session_repo.find_sessions(
            status=status, template_id=template_id, limit=limit, offset=offset
        )

        # Read the total
        total = await self._session_repo.count_sessions(status=status, template_id=template_id)

        # Convert to DTOs
        items = [SessionDTO.from_entity(s) for s in sessions]

        # Work out whether more remain
        has_more = (offset + len(items)) < total

        return {
            "items": items,
            "total": total,
            "limit": limit,
            "offset": offset,
            "has_more": has_more,
        }

    async def terminate_session(self, session_id: str) -> SessionDTO:
        """
        Terminate-session use case: a soft stop that keeps the record

        Steps:
        1. Find the session
        2. Validate its status
        3. Destroy the Docker container, when the scheduler supports it
        4. Clean up the S3 files, when a storage service is configured
        5. Update the session status
        """
        logger.info("Terminating session", session_id=session_id)

        session = await self._session_repo.find_by_id(session_id)
        if not session:
            logger.warning("Session not found for termination", session_id=session_id)
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        if session.is_terminated():
            logger.info(
                "Session already terminated", session_id=session_id, status=session.status.value
            )
            return SessionDTO.from_entity(session)

        logger.debug(
            "Terminating active session",
            session_id=session_id,
            container_id=session.container_id,
            status=session.status.value,
        )

        # Destroy the container
        await self._destroy_container(session)

        # Clean up the S3 files
        await self._cleanup_storage(session)

        # Update the session status
        session.mark_as_terminated()
        await self._session_repo.save(session)

        logger.info(
            "Session terminated successfully",
            session_id=session_id,
            final_status=session.status.value,
        )

        return SessionDTO.from_entity(session)

    async def delete_session(self, session_id: str) -> None:
        """
        Delete-session use case: a hard delete that cascades to the execution records

        Steps:
        1. Find the session
        2. Clean up: destroy the container and delete from S3
        3. Cascade-delete the database records
        """
        logger.info("Deleting session", session_id=session_id)

        session = await self._session_repo.find_by_id(session_id)
        if not session:
            logger.warning("Session not found for deletion", session_id=session_id)
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=session_id))

        logger.debug(
            "Deleting session",
            session_id=session_id,
            container_id=session.container_id,
            status=session.status.value,
        )

        # Destroy the container
        await self._destroy_container(session)

        # Clean up the S3 files
        await self._cleanup_storage(session)

        # Cascade-delete the database records: session plus executions
        await self._session_repo.delete(session_id)

        logger.info("Session deleted successfully", session_id=session_id)

    async def _destroy_container(self, session: Session) -> None:
        """Destroy the container of a session"""
        if not session.container_id or not hasattr(self._scheduler, "destroy_container"):
            return

        try:
            logger.info(
                "Destroying container", session_id=session.id, container_id=session.container_id
            )
            await self._scheduler.destroy_container(container_id=session.container_id)
            logger.info(
                "Container destroyed successfully",
                session_id=session.id,
                container_id=session.container_id,
            )
        except Exception as e:
            logger.warning(
                "Failed to destroy container",
                session_id=session.id,
                container_id=session.container_id,
                error=str(e),
            )

    async def _cleanup_storage(self, session: Session) -> None:
        """Clean up the stored files of a session"""
        if not self._storage_service or not session.workspace_path.startswith("s3://"):
            return

        try:
            logger.info(
                "Cleaning up S3 workspace files",
                session_id=session.id,
                workspace_path=session.workspace_path,
            )
            deleted_count = await self._storage_service.delete_prefix(session.workspace_path)
            logger.info(
                "S3 files deleted",
                session_id=session.id,
                deleted_count=deleted_count,
                workspace_path=session.workspace_path,
            )
        except Exception as e:
            logger.warning(
                "Failed to cleanup S3 files",
                session_id=session.id,
                workspace_path=session.workspace_path,
                error=str(e),
            )

    async def execute_code(self, command: ExecuteCodeCommand) -> ExecutionDTO:
        """
        Execute-code use case

        Steps:
        1. Verify the session exists and is running
        2. Generate the execution id
        3. Build the execution entity
        4. Persist it
        5. Submit it to the executor
        """
        logger.info(
            "Executing code",
            session_id=command.session_id,
            language=command.language,
            code_length=len(command.code),
        )

        # 1. Verify the session
        session = await self._session_repo.find_by_id(command.session_id)
        if not session:
            logger.error(
                "Session not found for execution",
                session_id=command.session_id,
            )
            raise NotFoundError(message("Sandbox.Session.NotFound", session_id=command.session_id))

        if not session.is_active():
            logger.warning(
                "Session is not active",
                session_id=command.session_id,
                status=session.status.value,
            )
            raise ValidationError(message("Sandbox.Session.NotActive", session_id=command.session_id))

        logger.debug(
            "Session validated for execution",
            session_id=command.session_id,
            container_id=session.container_id,
        )

        # 2. Generate the execution id
        execution_id = self._generate_execution_id()

        logger.debug(
            "Generated execution ID",
            execution_id=execution_id,
            session_id=command.session_id,
        )

        # 3. Build the execution entity
        from src.domain.value_objects.execution_status import ExecutionState

        execution = Execution(
            id=execution_id,
            session_id=command.session_id,
            code=command.code,
            language=command.language,
            timeout=command.timeout,
            event_data=command.event_data or {},
            state=ExecutionState(status=ExecutionStatus.PENDING),
        )

        # 4. Persist it
        await self._execution_repo.save(execution)
        logger.debug(
            "Execution saved to repository",
            execution_id=execution_id,
        )

        # 4.5. Commit, so the execution record is visible before the executor calls back
        await self._execution_repo.commit()

        # 5. Submit to the executor
        if not session.container_id:
            logger.error(
                "Session has no container",
                session_id=command.session_id,
            )
            raise ValidationError(message("Sandbox.Session.NoContainer", session_id=command.session_id))

        # Build the execution request
        execution_request = ExecutionRequest(
            code=command.code,
            language=command.language,
            event=command.event_data or {},
            timeout=command.timeout or 300,
            # The value from session creation is the floor; what this execution sends overrides it.
            # A pooled session still holds the previous caller's identity, and without
            # the override the current function would read that.
            env_vars={**(session.env_vars or {}), **(command.env_vars or {})},
            execution_id=execution_id,
            session_id=session.id,
            working_directory=command.working_directory,
        )

        logger.info(
            "Submitting execution to executor",
            execution_id=execution_id,
            session_id=command.session_id,
            container_id=session.container_id,
            timeout=execution_request.timeout,
        )

        # Submit to the executor through the scheduler
        await self._scheduler.execute(
            session_id=session.id,
            container_id=session.container_id,
            execution_request=execution_request,
        )

        logger.info(
            "Execution submitted successfully",
            execution_id=execution_id,
            session_id=command.session_id,
        )

        return ExecutionDTO.from_entity(execution)

    async def get_execution(self, query: GetExecutionQuery) -> ExecutionDTO:
        """Get-execution use case"""
        execution = await self._execution_repo.find_by_id(query.execution_id)
        if not execution:
            raise NotFoundError(message("Sandbox.Execution.NotFound", execution_id=query.execution_id))

        return ExecutionDTO.from_entity(execution)

    async def list_executions(
        self, session_id: str, limit: int = 50, offset: int = 0
    ) -> List[ExecutionDTO]:
        """List-executions-of-a-session use case"""
        executions = await self._execution_repo.find_by_session_id(
            session_id=session_id, limit=limit
        )

        return [ExecutionDTO.from_entity(e) for e in executions]

    async def cleanup_idle_sessions(
        self, idle_threshold_minutes: int = 30, max_lifetime_hours: int = 6
    ) -> int:
        """
        Clean-up-idle-sessions use case

        Called by the scheduled task to reclaim idle or expired sessions.
        """
        idle_threshold = datetime.now() - timedelta(minutes=idle_threshold_minutes)
        max_lifetime = datetime.now() - timedelta(hours=max_lifetime_hours)

        idle_sessions = await self._session_repo.find_idle_sessions(idle_threshold)
        expired_sessions = await self._session_repo.find_expired_sessions(max_lifetime)

        all_to_cleanup = set(idle_sessions + expired_sessions)
        cleaned_count = 0

        for session in all_to_cleanup:
            if await self._cleanup_session(session):
                cleaned_count += 1

        return cleaned_count

    async def _cleanup_session(self, session: Session) -> bool:
        """Clean up one session"""
        if not session.is_active():
            return False

        # Destroy the container
        if session.container_id and hasattr(self._scheduler, "destroy_container"):
            try:
                await self._scheduler.destroy_container(container_id=session.container_id)
            except Exception as e:
                logger.warning(
                    "Failed to destroy container during cleanup",
                    session_id=session.id,
                    container_id=session.container_id,
                    error=str(e),
                )

        session.mark_as_terminated()
        await self._session_repo.save(session)
        return True

    async def _sync_session_dependencies(
        self,
        session: Session,
        sync_mode: str,
        executor_timeout: int | None = None,
    ) -> SessionDTO:
        """Sync the session dependency configuration to the executor."""
        if not session.is_active():
            raise ValidationError(message("Sandbox.Session.NotActive", session_id=session.id))
        if not session.container_id:
            raise ValidationError(message("Sandbox.Session.NoContainer", session_id=session.id))
        if not hasattr(self._scheduler, "get_executor_url"):
            raise ValidationError(message("Sandbox.Scheduler.NoExecutorUrlDiscovery"))

        session.mark_dependency_installing()
        await self._session_repo.save(session)

        try:
            executor_url = await self._scheduler.get_executor_url(session.container_id)
            result = await self._executor_client.sync_session_config(
                executor_url=executor_url,
                session_id=session.id,
                language_runtime=session.runtime_type,
                python_package_index_url=session.python_package_index_url,
                dependencies=session.requested_dependencies,
                sync_mode=sync_mode,
                executor_timeout=executor_timeout,
            )
        except (
            ExecutorConnectionError,
            ExecutorTimeoutError,
            ExecutorUnavailableError,
            ExecutorValidationError,
            ExecutorResponseError,
        ) as error:
            session.mark_dependency_install_failed(str(error))
            await self._session_repo.save(session)
            raise

        installed_dependencies = [
            InstalledDependency(
                name=dep.name,
                version=dep.version,
                install_location=dep.install_location,
                install_time=datetime.fromisoformat(dep.install_time.replace("Z", "+00:00")),
                is_from_template=dep.is_from_template,
            )
            for dep in result.installed_dependencies
        ]

        completed_at = None
        if result.completed_at:
            completed_at = datetime.fromisoformat(result.completed_at.replace("Z", "+00:00"))

        session.mark_dependency_install_completed(
            installed_dependencies,
            completed_at=completed_at,
        )
        if result.started_at:
            session.dependency_install_started_at = datetime.fromisoformat(
                result.started_at.replace("Z", "+00:00")
            )
        await self._session_repo.save(session)
        return SessionDTO.from_entity(session)

    def _schedule_initial_dependency_sync(
        self,
        session_id: str,
        install_timeout: int,
        dependency_count: int,
    ) -> None:
        """Schedule the background task for the first dependency install."""
        if self._initial_dependency_sync_scheduler is None:
            logger.warning(
                "Initial dependency sync scheduler is not configured",
                session_id=session_id,
                dependency_count=dependency_count,
            )
            return

        logger.info(
            "Scheduling initial dependency sync",
            session_id=session_id,
            dependency_count=dependency_count,
            install_timeout=install_timeout,
        )
        self._initial_dependency_sync_scheduler(session_id, install_timeout)

    def _generate_session_id(self) -> str:
        """Generate the session id"""
        timestamp = datetime.now().strftime("%Y%m%d")
        unique = uuid.uuid4().hex[:8]
        return f"sess_{timestamp}_{unique}"

    def _infer_runtime_type(self, image: str) -> str:
        """Infer the runtime type from the image name"""
        image_lower = image.lower()
        if "python" in image_lower or "python3" in image_lower:
            return "python3.11"
        elif "node" in image_lower or "nodejs" in image_lower:
            return "nodejs20"
        elif "java" in image_lower:
            return "java17"
        elif "go" in image_lower or "golang" in image_lower:
            return "go1.21"
        else:
            # Default to Python
            return "python3.11"

    def _generate_execution_id(self) -> str:
        """Generate the execution id"""
        timestamp = datetime.now().strftime("%Y%m%d%H%M%S")
        unique = uuid.uuid4().hex[:8]
        return f"exec_{timestamp}_{unique}"
