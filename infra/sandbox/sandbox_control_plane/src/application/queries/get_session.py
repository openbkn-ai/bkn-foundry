"""
Get-session query

The query object for reading a session.
"""

from dataclasses import dataclass


@dataclass
class GetSessionQuery:
    """Get-session query"""

    session_id: str

    def __post_init__(self):
        """Validate after construction"""
        if not self.session_id:
            raise ValueError("session_id cannot be empty")
