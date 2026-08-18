"""
Infrastructure errors

The error types of the infrastructure layer.
"""

from typing import Optional


class InfrastructureError(Exception):
    """Base infrastructure error"""

    def __init__(self, message: str, original_error: Optional[Exception] = None):
        self.message = message
        self.original_error = original_error
        super().__init__(self.message)


class DatabaseError(InfrastructureError):
    """Database error"""

    pass


class ConnectionError(InfrastructureError):
    """Connection error"""

    pass


class StorageError(InfrastructureError):
    """Storage error"""

    pass


class HTTPClientError(InfrastructureError):
    """HTTP client error"""

    pass


class ContainerError(InfrastructureError):
    """Container error"""

    pass


class KubernetesError(InfrastructureError):
    """Kubernetes error"""

    pass


class MessagingError(InfrastructureError):
    """Message queue error"""

    pass
