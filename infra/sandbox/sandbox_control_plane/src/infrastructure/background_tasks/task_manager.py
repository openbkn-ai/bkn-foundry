"""
Background task manager

Starts and stops periodic background tasks, with graceful shutdown.
"""

import asyncio
import logging
from contextlib import asynccontextmanager
from typing import Callable, List, Optional

logger = logging.getLogger(__name__)


class BackgroundTask:
    """
    Background task

    One task that runs periodically.
    """

    def __init__(
        self,
        name: str,
        func: Callable,
        interval_seconds: int,
        initial_delay_seconds: int = 0,
    ):
        """
        Initialize the background task

        Args:
            name: task name
            func: the async function the task runs
            interval_seconds: how often to run, in seconds
            initial_delay_seconds: how long to wait before the first run, in seconds
        """
        self.name = name
        self.func = func
        self.interval_seconds = interval_seconds
        self.initial_delay_seconds = initial_delay_seconds
        self._task: Optional[asyncio.Task] = None
        self._stop_event = asyncio.Event()
        self._running = False

    async def start(self) -> None:
        """
        Start the background task

        Does nothing when the task is already running.
        """
        if self._running:
            logger.warning(f"Task {self.name} is already running")
            return

        self._stop_event.clear()
        self._running = True
        self._task = asyncio.create_task(self._run())
        logger.info(f"Started background task: {self.name}")

    async def stop(self) -> None:
        """
        Stop the background task

        Waits up to 30 seconds for the current run to finish.
        """
        if not self._running:
            return

        self._stop_event.set()
        self._running = False

        try:
            await asyncio.wait_for(self._task, timeout=30)
            logger.info(f"Stopped background task: {self.name}")
        except asyncio.TimeoutError:
            logger.warning(f"Task {self.name} did not stop gracefully, cancelling")
            self._task.cancel()

    async def _run(self) -> None:
        """
        The task loop

        What it does:
        1. Wait out the initial delay, when one is configured
        2. Then loop:
           - run the task function
           - wait for the interval, or for the stop event
        """
        try:
            # Initial delay
            if self.initial_delay_seconds > 0:
                await asyncio.sleep(self.initial_delay_seconds)

            # The task loop
            while not self._stop_event.is_set():
                try:
                    # Run the task function
                    await self.func()
                except Exception as e:
                    logger.error(
                        f"Error in background task {self.name}: {e}",
                        exc_info=True,
                    )

                # Wait for the interval, or for the stop event
                try:
                    await asyncio.wait_for(
                        self._stop_event.wait(),
                        timeout=self.interval_seconds,
                    )
                    # wait_for completing means the stop event was set
                    break
                except asyncio.TimeoutError:
                    # Timed out, so keep looping
                    continue

        except asyncio.CancelledError:
            logger.info(f"Background task {self.name} was cancelled")
        except Exception as e:
            logger.error(
                f"Unexpected error in background task {self.name}: {e}",
                exc_info=True,
            )

    @property
    def is_running(self) -> bool:
        """Check whether the task is running"""
        return self._running and self._task is not None and not self._task.done()


class BackgroundTaskManager:
    """
    Background task manager

    Starts and stops several background tasks.
    """

    def __init__(self):
        self._tasks: List[BackgroundTask] = []
        self._running = False

    def register_task(
        self,
        name: str,
        func: Callable,
        interval_seconds: int,
        initial_delay_seconds: int = 0,
    ) -> None:
        """
        Register a new background task

        Args:
            name: task name
            func: the async function the task runs
            interval_seconds: how often to run, in seconds
            initial_delay_seconds: how long to wait before the first run, in seconds
        """
        task = BackgroundTask(
            name=name,
            func=func,
            interval_seconds=interval_seconds,
            initial_delay_seconds=initial_delay_seconds,
        )
        self._tasks.append(task)
        logger.info(
            f"Registered background task: {name} "
            f"(interval: {interval_seconds}s, delay: {initial_delay_seconds}s)"
        )

    async def start_all(self) -> None:
        """
        Start every registered background task

        Does nothing for a task that is already running.
        """
        if self._running:
            logger.warning("Background tasks already running")
            return

        self._running = True

        for task in self._tasks:
            await task.start()

        logger.info(f"Started {len(self._tasks)} background tasks")

    async def stop_all(self) -> None:
        """
        Stop every running background task

        Waits up to 30 seconds for the current runs to finish.
        """
        if not self._running:
            return

        self._running = False

        # Stop them in parallel
        tasks_to_stop = [task.stop() for task in self._tasks]
        await asyncio.gather(*tasks_to_stop, return_exceptions=True)

        logger.info("Stopped all background tasks")

    @asynccontextmanager
    async def lifecycle(self):
        """
        Task lifecycle context manager

        Usage:
            async with task_manager.lifecycle():
                # the tasks are running
                pass
            # the tasks have stopped
        """
        await self.start_all()
        try:
            yield
        finally:
            await self.stop_all()

    @property
    def running(self) -> bool:
        """Check whether the manager is running"""
        return self._running

    @property
    def task_count(self) -> int:
        """How many tasks are registered"""
        return len(self._tasks)

    def get_task_status(self) -> dict:
        """
        Get the status of every task

        Returns:
            dict: task name to whether it is running
        """
        return {task.name: task.is_running for task in self._tasks}
