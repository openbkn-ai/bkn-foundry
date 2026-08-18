"""
Domain errors

The error types of the domain layer.
"""

from typing import Any, Optional


class DomainError(Exception):
    """Base domain error"""

    def __init__(self, message: str, details: Optional[dict[str, Any]] = None):
        self.message = message
        self.details = details or {}
        super().__init__(self.message)


class NotFoundError(DomainError):
    """Not found"""

    pass


class ValidationError(DomainError):
    """Validation failed"""

    pass


class InvalidStatusError(DomainError):
    """Invalid state"""

    pass


class ResourceLimitError(DomainError):
    """Resource limit exceeded"""

    pass


class SessionExpiredError(DomainError):
    """Session expired"""

    pass


class ExecutionTimeoutError(DomainError):
    """Execution timed out"""

    pass


class ExecutionCrashedError(DomainError):
    """Execution crashed"""

    pass


class TemplateNotFoundError(DomainError):
    """Template not found"""

    pass


class NodeUnavailableError(DomainError):
    """Node unavailable"""

    pass


class ConflictError(DomainError):
    """Resource conflict"""

    pass
