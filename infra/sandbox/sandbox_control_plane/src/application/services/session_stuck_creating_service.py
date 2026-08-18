"""
Session creation timeout service

Periodically finds sessions stuck in creating past a threshold and marks them failed.
This covers a failed executor container start-up, such as a failed dependency install,
which would otherwise leave the session in creating forever.
"""

import logging
from datetime import datetime, timedelta
from typing import Dict, Optional

from src.domain.entities.session import Session, SessionStatus
from src.domain.repositories.session_repository import ISessionRepository

logger = logging.getLogger(__name__)


class SessionStuckCreatingService:
    """
    Session creation timeout service

    Responsibilities:
    1. Periodically scan for sessions in "creating"
    2. Check whether creation started longer ago than the configured timeout
    3. Mark a timed-out session "failed"

    The policy:
    - Default timeout: 300 seconds, five minutes, configurable
    - Scan interval: matches cleanup_interval_seconds by default, five minutes
    """

    def __init__(
        self,
        session_repo: ISessionRepository,
        creating_timeout_seconds: int = 300,
    ):
        """
        Initialize the session creation timeout service

        Args:
            session_repo: session repository
            creating_timeout_seconds: creation timeout in seconds, at least 30
        """
        self._session_repo = session_repo
        self._timeout = timedelta(seconds=creating_timeout_seconds)

    async def check_and_mark_stuck_sessions(self) -> Dict[str, int]:
        """
        Find and mark the sessions stuck in creating

        Returns:
            dict: the scan statistics
                - total_checked: how many sessions were examined
                - marked_failed: how many were marked failed
                - errors: the error list
        """
        stats = {"total_checked": 0, "marked_failed": 0, "errors": []}

        try:
            now = datetime.now()
            timeout_threshold = now - self._timeout

            # Query every session in "creating"
            creating_sessions = await self._session_repo.find_by_status(SessionStatus.CREATING)
            stats["total_checked"] = len(creating_sessions)

            if creating_sessions:
                logger.info(
                    f"Checking {len(creating_sessions)} sessions in 'creating' status, "
                    f"timeout_threshold={timeout_threshold.isoformat()}"
                )

            for session in creating_sessions:
                try:
                    # Check whether creation started longer ago than the threshold
                    if session.created_at and session.created_at < timeout_threshold:
                        await self._mark_session_as_failed(
                            session,
                            reason="creating_timeout",
                            detail=f"Session stuck in 'creating' status for {(now - session.created_at).total_seconds():.0f} seconds "
                            f"(timeout: {self._timeout.total_seconds():.0f}s)",
                        )
                        stats["marked_failed"] += 1
                    else:
                        # Log the ones still inside the window, for debugging
                        if session.created_at:
                            time_in_creating = (now - session.created_at).total_seconds()
                            logger.debug(
                                f"Session {session.id} has been in 'creating' status for {time_in_creating:.0f}s "
                                f"(timeout: {self._timeout.total_seconds():.0f}s)"
                            )

                except Exception as e:
                    error_msg = f"Error processing session {session.id}: {e}"
                    logger.error(error_msg, exc_info=True)
                    stats["errors"].append(error_msg)

            if stats["marked_failed"] > 0:
                logger.info(
                    f"Session stuck-creating check completed: "
                    f"checked={stats['total_checked']}, "
                    f"marked_failed={stats['marked_failed']}"
                )

        except Exception as e:
            error_msg = f"Fatal error during stuck-creating session check: {e}"
            logger.error(error_msg, exc_info=True)
            stats["errors"].append(error_msg)

        return stats

    async def _mark_session_as_failed(self, session: Session, reason: str, detail: str) -> None:
        """
        Mark the session failed

        Args:
            session: the session to mark
            reason: why it failed
            detail: further detail
        """
        logger.warning(
            f"Marking session {session.id} as failed: "
            f"reason={reason}, "
            f"detail={detail}, "
            f"container_id={session.container_id}, "
            f"created_at={session.created_at}"
        )

        # Mark the session failed
        session.mark_as_failed()
        await self._session_repo.save(session)

        logger.info(f"Session {session.id} marked as failed")
