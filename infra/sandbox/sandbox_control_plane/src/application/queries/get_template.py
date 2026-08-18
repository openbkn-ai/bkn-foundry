"""
Get-template query

The query DTO for reading the template details.
"""

from dataclasses import dataclass


@dataclass
class GetTemplateQuery:
    """Get-template query"""

    template_id: str
