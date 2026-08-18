"""
Session cleanup service

Periodically reclaims idle and expired sessions, destroying the container and deleting the files that belong to them.
"""

import logging
from datetime import datetime, timedelta
from typing import Dict, Optional
from urllib.parse import urlparse

from src.domain.entities.session import Session, SessionStatus
from src.domain.repositories.session_repository import ISessionRepository
from src.domain.services.scheduler import IScheduler
from src.domain.services.storage import IStorageService

logger = logging.getLogger(__name__)


class SessionCleanupService:
    """
    Session cleanup service

    Responsibilities:
    1. Periodically scan for idle sessions, using the last_activity_at field
    2. Terminate timed-out sessions and destroy their containers
    3. Periodically scan for orphaned sessions left in FAILED or TIMEOUT
    4. Delete the S3 files that belong to a session

    The policy:
    - Idle timeout: 30 minutes without activity, configurable, -1 disables idle cleanup
    - Maximum lifetime: forced cleanup after 6 hours, configurable, -1 disables it
    """

    def __init__(
        self,
        session_repo: ISessionRepository,
        scheduler: IScheduler,
        idle_timeout_minutes: int = 30,
        max_lifetime_hours: int = 6,
        storage_service: Optional[IStorageService] = None,
    ):
        """
        Initialize the session cleanup service

        Args:
            session_repo: session repository
            scheduler: scheduler, used to destroy containers
            idle_timeout_minutes: idle timeout in minutes; -1 means never, disabling idle cleanup
            max_lifetime_hours: maximum lifetime in hours; -1 means never
            storage_service: storage service, optional, used to delete S3 files
        """
        self._session_repo = session_repo
        self._scheduler = scheduler
        self._storage_service = storage_service
        self._idle_timeout = (
            None if idle_timeout_minutes == -1 else timedelta(minutes=idle_timeout_minutes)
        )
        self._max_lifetime = (
            None if max_lifetime_hours == -1 else timedelta(hours=max_lifetime_hours)
        )

    async def cleanup_idle_sessions(self) -> Dict[str, int]:
        """
        Clean up idle sessions

        The policy:
        - A session idle beyond the threshold has its container destroyed, when idle_timeout_minutes != -1
        - A session older than the maximum lifetime is destroyed, when max_lifetime_hours != -1

        Returns:
            dict: the cleanup statistics
                - total_checked: how many sessions were examined
                - idle_cleaned: how many were reclaimed as idle
                - expired_cleaned: how many were reclaimed as expired
                - errors: the error list
        """
        stats = {"total_checked": 0, "idle_cleaned": 0, "expired_cleaned": 0, "errors": []}

        try:
            now = datetime.now()
            idle_threshold = now - self._idle_timeout if self._idle_timeout else None
            max_lifetime_threshold = now - self._max_lifetime if self._max_lifetime else None

            # Query every active session
            active_sessions = await self._session_repo.find_by_status("running")
            stats["total_checked"] = len(active_sessions)

            logger.info(
                f"Starting session cleanup: checking {len(active_sessions)} active sessions, "
                f"idle_threshold={idle_threshold}, max_lifetime={max_lifetime_threshold}"
            )

            for session in active_sessions:
                try:
                    # Check the maximum lifetime, when that is enabled
                    if (
                        max_lifetime_threshold
                        and session.created_at
                        and session.created_at < max_lifetime_threshold
                    ):
                        await self._cleanup_session(
                            session,
                            reason="max_lifetime_exceeded",
                            detail=f"Session created at {session.created_at} exceeded max lifetime of {self._max_lifetime}",
                        )
                        stats["expired_cleaned"] += 1
                        continue

                    # Check the idle timeout, when that is enabled.
                    # Use last_activity_at, falling back to created_at.
                    if idle_threshold:
                        last_activity = session.last_activity_at or session.created_at
                        if last_activity and last_activity < idle_threshold:
                            await self._cleanup_session(
                                session,
                                reason="idle_timeout",
                                detail=f"Session last activity at {last_activity} exceeded idle timeout of {self._idle_timeout}",
                            )
                            stats["idle_cleaned"] += 1
                            continue

                except Exception as e:
                    error_msg = f"Error cleaning session {session.id}: {e}"
                    logger.error(error_msg, exc_info=True)
                    stats["errors"].append(error_msg)

            logger.info(
                f"Session cleanup completed: "
                f"checked={stats['total_checked']}, "
                f"idle_cleaned={stats['idle_cleaned']}, "
                f"expired_cleaned={stats['expired_cleaned']}"
            )

        except Exception as e:
            error_msg = f"Fatal error during session cleanup: {e}"
            logger.error(error_msg, exc_info=True)
            stats["errors"].append(error_msg)

        return stats

    async def cleanup_orphaned_sessions(self) -> Dict[str, int]:
        """
        Clean up orphaned sessions: left in FAILED or TIMEOUT while still holding a container

        Returns:
            dict: the cleanup statistics
        """
        stats = {"total_checked": 0, "cleaned": 0, "errors": []}

        try:
            # Query the failed and timed-out sessions
            failed_sessions = await self._session_repo.find_by_status("failed")
            timeout_sessions = await self._session_repo.find_by_status("timeout")
            orphaned = failed_sessions + timeout_sessions

            stats["total_checked"] = len(orphaned)

            for session in orphaned:
                # Only those that still hold a container_id
                if session.container_id:
                    try:
                        await self._cleanup_session(
                            session,
                            reason="orphaned_cleanup",
                            detail=f"Session in {session.status} status with container {session.container_id}",
                        )
                        stats["cleaned"] += 1
                    except Exception as e:
                        error_msg = f"Error cleaning orphaned session {session.id}: {e}"
                        logger.error(error_msg, exc_info=True)
                        stats["errors"].append(error_msg)

            logger.info(
                f"Orphaned session cleanup completed: "
                f"checked={stats['total_checked']}, "
                f"cleaned={stats['cleaned']}"
            )

        except Exception as e:
            error_msg = f"Fatal error during orphaned session cleanup: {e}"
            logger.error(error_msg, exc_info=True)
            stats["errors"].append(error_msg)

        return stats

    async def cleanup_session_files(self, session: Session, reason: str) -> int:
        """
        Delete every file that belongs to a session

        Args:
            session: the session to clean up
            reason: why it is being cleaned up

        Returns:
            How many files were deleted
        """
        if not self._storage_service:
            logger.debug(
                f"Storage service not configured, skipping file cleanup for session {session.id}"
            )
            return 0

        if not session.workspace_path:
            logger.debug(f"Session {session.id} has no workspace_path, skipping file cleanup")
            return 0

        logger.info(
            f"Cleaning up files for session {session.id} "
            f"(reason: {reason}, workspace: {session.workspace_path})"
        )

        try:
            # Pull the bucket and prefix out of workspace_path,
            # which is formatted as s3://bucket/sessions/{session_id}/
            parsed = urlparse(session.workspace_path)
            bucket = parsed.netloc  # bucket name

            # Build the delete prefix, including the bucket path,
            # for example s3://sandbox-workspace/sessions/sess_abc123/
            delete_prefix = session.workspace_path.rstrip("/")

            # Delete everything under that prefix
            deleted_count = await self._storage_service.delete_prefix(delete_prefix)

            logger.info(
                f"Deleted {deleted_count} files for session {session.id} "
                f"(prefix: {delete_prefix})"
            )

            return deleted_count

        except Exception as e:
            logger.error(f"Error cleaning up files for session {session.id}: {e}", exc_info=True)
            return 0

    async def _cleanup_session(self, session: Session, reason: str, detail: str) -> None:
        """
        Clean up the session and destroy its container

        Args:
            session: the session to clean up
            reason: why it is being cleaned up
            detail: further detail
        """
        logger.info(
            f"Cleaning up session {session.id}: "
            f"reason={reason}, "
            f"detail={detail}, "
            f"container_id={session.container_id}"
        )

        # Destroy the container, when the scheduler supports it and one exists
        if session.container_id and hasattr(self._scheduler, "destroy_container"):
            try:
                await self._scheduler.destroy_container(container_id=session.container_id)
                logger.info(f"Destroyed container {session.container_id} for session {session.id}")
            except Exception as e:
                # Record the error but keep going
                logger.warning(
                    f"Failed to destroy container {session.container_id} for session {session.id}: {e}"
                )

        # Delete the S3 files, when a storage service is configured
        await self.cleanup_session_files(session, reason)

        # Mark the session terminated
        session.mark_as_terminated()
        await self._session_repo.save(session)

        logger.info(f"Session {session.id} marked as terminated")

    async def cleanup_by_ids(self, session_ids: list[str]) -> Dict[str, int]:
        """
        Clean up the sessions named by a list of ids

        Args:
            session_ids: the session ids to clean up

        Returns:
            dict: the cleanup statistics
        """
        stats = {"total": len(session_ids), "cleaned": 0, "not_found": 0, "errors": []}

        for session_id in session_ids:
            try:
                session = await self._session_repo.find_by_id(session_id)
                if not session:
                    stats["not_found"] += 1
                    continue

                await self._cleanup_session(
                    session, reason="manual_cleanup", detail=f"Manual cleanup requested"
                )
                stats["cleaned"] += 1

            except Exception as e:
                error_msg = f"Error cleaning session {session_id}: {e}"
                logger.error(error_msg, exc_info=True)
                stats["errors"].append(error_msg)

        logger.info(
            f"Manual session cleanup completed: "
            f"total={stats['total']}, "
            f"cleaned={stats['cleaned']}, "
            f"not_found={stats['not_found']}"
        )

        return stats
