"""Unit tests for conftest."""
import asyncio
import time


def pytest_configure(config):
    """Create pytest configure."""
    # Disable parallel tests.
    config.pluginmanager.set_blocked("pytest-xdist")


def pytest_runtest_setup(item):
    """Create pytest runtest setup."""
    # Add a delay to avoid starting multiple containers simultaneously.
    time.sleep(0.5)


def pytest_runtest_teardown(item, nextitem):
    """Create pytest runtest teardown."""
    # Ensure async resources are cleaned up.
    try:
        loop = asyncio.get_event_loop()
        if loop and not loop.is_closed():
            # Run all pending tasks.
            pending = asyncio.all_tasks(loop)
            if pending:
                loop.run_until_complete(asyncio.gather(*pending, return_exceptions=True))
    except Exception:
        pass
