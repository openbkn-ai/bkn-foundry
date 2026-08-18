"""
Executor HTTP client

Talks over HTTP to the executor inside a sandbox container.
"""

import asyncio
import logging
from typing import Optional

import httpx

from src.infrastructure.executors.dto import (
    ExecutorExecuteRequest,
    ExecutorExecuteResponse,
    ExecutorHealthResponse,
    ExecutorSyncSessionConfigRequest,
    ExecutorSyncSessionConfigResponse,
)
from src.infrastructure.executors.errors import (
    ExecutorConnectionError,
    ExecutorTimeoutError,
    ExecutorUnavailableError,
    ExecutorResponseError,
    ExecutorValidationError,
)

logger = logging.getLogger(__name__)


class ExecutorClient:
    """
    Executor HTTP client

    Talks over HTTP to the sandbox-executor running in the container.
    """

    def __init__(
        self,
        timeout: float = 30.0,
        max_retries: int = 3,
        retry_delay: float = 0.5,
    ):
        """
        Initialize the executor client

        Args:
            timeout: request timeout in seconds
            max_retries: how many times to retry
            retry_delay: delay between retries, in seconds
        """
        self._timeout = timeout
        self._max_retries = max_retries
        self._retry_delay = retry_delay
        self._client: Optional[httpx.AsyncClient] = None

    async def __aenter__(self):
        """Enter the context manager"""
        self._client = httpx.AsyncClient(timeout=self._timeout)
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        """Leave the context manager"""
        if self._client:
            await self._client.aclose()

    def _get_client(self) -> httpx.AsyncClient:
        """Get the HTTP client"""
        if self._client is None:
            self._client = httpx.AsyncClient(timeout=self._timeout)
        return self._client

    async def submit_execution(
        self,
        executor_url: str,
        execution_id: str,
        session_id: str,
        code: str,
        language: str,
        event: dict,
        timeout: int,
        env_vars: dict,
        working_directory: str | None = None,
    ) -> str:
        """
        Submit an execution request to the executor

        Args:
            executor_url: executor URL, such as "http://container-name:8080"
            execution_id: execution id
            session_id: session id
            code: the code to run
            language: programming language
            event: event payload
            timeout: timeout in seconds
            env_vars: environment variables
            working_directory: optional working directory, relative to the workspace root

        Returns:
            execution_id: execution task id

        Raises:
            ExecutorConnectionError: the executor is unreachable
            ExecutorTimeoutError: the executor did not answer in time
            ExecutorValidationError: the request failed validation
            ExecutorResponseError: the executor returned an error
        """
        client = self._get_client()
        url = f"{executor_url}/execute"

        request = ExecutorExecuteRequest(
            execution_id=execution_id,
            session_id=session_id,
            code=code,
            language=language,
            event=event,
            timeout=timeout,
            env_vars=env_vars,
            working_directory=working_directory,
        )

        logger.info(
            f"Submitting execution request: executor_url={executor_url}, execution_id={execution_id}, language={language}"
        )

        for attempt in range(self._max_retries):
            try:
                response = await client.post(
                    url,
                    json=request.model_dump(),
                    headers={"Content-Type": "application/json"},
                )

                if response.status_code == 200:
                    result = ExecutorExecuteResponse(**response.json())
                    logger.info(
                        f"Execution submitted successfully: execution_id={execution_id}, status={result.status}"
                    )
                    return result.execution_id

                elif response.status_code == 400:
                    # Validation error - don't retry
                    raise ExecutorValidationError(executor_url, response.json().get("errors", []))

                elif response.status_code >= 500:
                    # Server error - retry
                    if attempt < self._max_retries - 1:
                        logger.warning(
                            f"Executor returned {response.status_code}, retrying... attempt={attempt + 1}"
                        )
                        await asyncio.sleep(self._retry_delay * (attempt + 1))
                        continue
                    else:
                        raise ExecutorResponseError(
                            executor_url,
                            response.status_code,
                            response.text,
                        )

                else:
                    raise ExecutorResponseError(
                        executor_url,
                        response.status_code,
                        response.text,
                    )

            except httpx.ConnectError as e:
                if attempt < self._max_retries - 1:
                    logger.warning(
                        f"Failed to connect to executor, retrying... executor_url={executor_url}, attempt={attempt + 1}, error={e}"
                    )
                    await asyncio.sleep(self._retry_delay * (attempt + 1))
                    continue
                else:
                    raise ExecutorConnectionError(executor_url, str(e))

            except httpx.TimeoutException as e:
                raise ExecutorTimeoutError(executor_url, self._timeout)

            except httpx.HTTPStatusError as e:
                raise ExecutorResponseError(executor_url, e.response.status_code, str(e))

        # Should not reach here
        raise ExecutorConnectionError(executor_url, "Max retries exceeded")

    async def health_check(self, executor_url: str) -> ExecutorHealthResponse:
        """
        Check the executor health

        Args:
            executor_url: executor URL

        Returns:
            The health response

        Raises:
            ExecutorConnectionError: the executor is unreachable
            ExecutorUnavailableError: the executor is unhealthy
        """
        client = self._get_client()
        url = f"{executor_url}/health"

        try:
            response = await client.get(url)

            if response.status_code == 200:
                return ExecutorHealthResponse(**response.json())
            else:
                raise ExecutorUnavailableError(executor_url, f"status_code={response.status_code}")

        except httpx.ConnectError as e:
            raise ExecutorConnectionError(executor_url, str(e))
        except httpx.TimeoutException as e:
            raise ExecutorTimeoutError(executor_url, self._timeout)

    async def sync_session_config(
        self,
        executor_url: str,
        session_id: str,
        language_runtime: str,
        python_package_index_url: str,
        dependencies: list[str],
        sync_mode: str,
        executor_timeout: float | None = None,
    ) -> ExecutorSyncSessionConfigResponse:
        """Sync the session dependency configuration to the executor."""
        client = self._get_client()
        url = f"{executor_url}/internal/session-config/sync"
        effective_timeout = executor_timeout or self._timeout
        request = ExecutorSyncSessionConfigRequest(
            session_id=session_id,
            language_runtime=language_runtime,
            python_package_index_url=python_package_index_url,
            dependencies=dependencies,
            sync_mode=sync_mode,
        )

        try:
            response = await client.post(
                url,
                json=request.model_dump(),
                headers={"Content-Type": "application/json"},
                timeout=effective_timeout,
            )
        except httpx.ConnectError as e:
            raise ExecutorConnectionError(executor_url, str(e))
        except httpx.TimeoutException:
            raise ExecutorTimeoutError(executor_url, effective_timeout)

        if response.status_code == 200:
            return ExecutorSyncSessionConfigResponse(**response.json())
        if response.status_code == 400:
            raise ExecutorValidationError(executor_url, response.json())
        if response.status_code in (422, 500):
            raise ExecutorResponseError(executor_url, response.status_code, response.text)
        if response.status_code == 503:
            raise ExecutorUnavailableError(executor_url, response.text)

        raise ExecutorResponseError(executor_url, response.status_code, response.text)

    async def close(self) -> None:
        """Close the client"""
        if self._client:
            await self._client.aclose()
            self._client = None
