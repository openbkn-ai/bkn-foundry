"""
Get-execution query

The query DTO for reading the execution details.
"""

from dataclasses import dataclass


@dataclass
class GetExecutionQuery:
    """Get-execution query"""

    execution_id: str
