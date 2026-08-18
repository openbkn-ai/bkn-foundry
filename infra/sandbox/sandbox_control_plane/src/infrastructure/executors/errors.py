"""
Executor errors

The errors that can arise while talking to the executor.
"""


class ExecutorError(Exception):
    """Base executor error"""

    pass


class ExecutorConnectionError(ExecutorError):
    """The executor is unreachable"""

    def __init__(self, executor_url: str, reason: str = ""):
        self.executor_url = executor_url
        self.reason = reason
        super().__init__(f"Failed to connect to executor at {executor_url}: {reason}")


class ExecutorTimeoutError(ExecutorError):
    """The executor did not answer in time"""

    def __init__(self, executor_url: str, timeout: float):
        self.executor_url = executor_url
        self.timeout = timeout
        super().__init__(f"Executor at {executor_url} timed out after {timeout}s")


class ExecutorUnavailableError(ExecutorError):
    """The executor is unavailable, meaning unhealthy"""

    def __init__(self, executor_url: str, status: str = ""):
        self.executor_url = executor_url
        self.status = status
        super().__init__(f"Executor at {executor_url} is unavailable: {status}")


class ExecutorResponseError(ExecutorError):
    """The executor returned an error response"""

    def __init__(self, executor_url: str, status_code: int, message: str = ""):
        self.executor_url = executor_url
        self.status_code = status_code
        self.message = message
        super().__init__(f"Executor at {executor_url} returned error {status_code}: {message}")


class ExecutorValidationError(ExecutorError):
    """The executor request failed validation"""

    def __init__(self, executor_url: str, validation_errors: list):
        self.executor_url = executor_url
        self.validation_errors = validation_errors
        super().__init__(f"Executor at {executor_url} rejected request: {validation_errors}")
