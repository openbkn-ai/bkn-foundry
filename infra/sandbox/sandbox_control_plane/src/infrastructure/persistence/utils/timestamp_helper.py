"""
Timestamp helpers

Converts between BIGINT millisecond timestamps and datetime objects.
Per the table naming convention, timestamp columns store milliseconds as BIGINT.
"""

import time
from datetime import datetime
from typing import Optional


def datetime_to_millis(dt: Optional[datetime]) -> int:
    """
    Turn a datetime into a millisecond timestamp

    Args:
        dt: the datetime; None means the current time

    Returns:
        The millisecond timestamp
    """
    if dt is None:
        return int(time.time() * 1000)
    return int(dt.timestamp() * 1000)


def millis_to_datetime(millis: Optional[int]) -> Optional[datetime]:
    """
    Turn a millisecond timestamp into a datetime

    Args:
        millis: the millisecond timestamp; None or 0 returns None

    Returns:
        The datetime, or None when the input is invalid
    """
    if not millis or millis == 0:
        return None
    try:
        return datetime.fromtimestamp(millis / 1000)
    except (ValueError, OSError):
        return None


def current_millis() -> int:
    """
    The current time as a millisecond timestamp

    Returns:
        The current millisecond timestamp
    """
    return int(time.time() * 1000)


def current_datetime() -> datetime:
    """
    The current time as a datetime

    Returns:
        The current datetime
    """
    return datetime.now()
