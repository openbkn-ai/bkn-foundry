"""Unit tests for conftest."""
import asyncio
import time


def pytest_configure(config):
    """Create pytest configure."""
    # Test setup.
    config.pluginmanager.set_blocked("pytest-xdist")


def pytest_runtest_setup(item):
    """Create pytest runtest setup."""
    # Test setup.
    time.sleep(0.5)


def pytest_runtest_teardown(item, nextitem):
    """Create pytest runtest teardown."""
    # Test setup.
    try:
        loop = asyncio.get_event_loop()
        if loop and not loop.is_closed():
            # Test setup.
            pending = asyncio.all_tasks(loop)
            if pending:
                loop.run_until_complete(asyncio.gather(*pending, return_exceptions=True))
    except Exception:
        pass
