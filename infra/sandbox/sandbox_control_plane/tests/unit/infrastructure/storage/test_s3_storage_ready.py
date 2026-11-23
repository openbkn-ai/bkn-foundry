"""Regression tests for #3327: bucket readiness after a failed startup init."""
import asyncio
import time
from unittest.mock import AsyncMock, Mock, patch

import pytest
from botocore.exceptions import ClientError, EndpointConnectionError

from src.application.commands.create_session import CreateSessionCommand
from src.application.services.session_service import SessionService
from src.infrastructure.container_scheduler.docker_scheduler import DockerScheduler
from src.infrastructure.container_scheduler.k8s_scheduler import K8sScheduler
from src.infrastructure.storage import s3_storage
from src.infrastructure.storage.s3_storage import S3Storage
from src.shared.errors.infrastructure import StorageError


def _client_error(code: str, op: str) -> ClientError:
    return ClientError({"Error": {"Code": code}}, op)


def _conn_error() -> EndpointConnectionError:
    return EndpointConnectionError(endpoint_url="http://minio:9000")


@pytest.fixture
def client():
    c = Mock()
    c.meta.region_name = "us-east-1"
    return c


@pytest.fixture
def storage(client):
    settings = Mock()
    settings.s3_endpoint_url = "http://minio:9000"
    settings.s3_access_key_id = "k"
    settings.s3_secret_access_key = "s"
    settings.s3_region = "us-east-1"
    settings.s3_bucket = "sandbox-workspace"
    with patch.object(s3_storage.boto3, "client", return_value=client):
        with patch.object(s3_storage, "get_settings", return_value=settings):
            return S3Storage()


@pytest.fixture(autouse=True)
def no_sleep():
    with patch.object(s3_storage.asyncio, "sleep", new=AsyncMock()) as sleep:
        yield sleep


class TestInitializeRetry:
    async def test_init_retries_connection_errors_then_succeeds(self, storage, client, no_sleep):
        client.head_bucket.side_effect = [_conn_error(), _conn_error(), {}]

        await storage.initialize()

        assert client.head_bucket.call_count == 3
        assert storage.is_ready
        assert no_sleep.await_count == 2

    async def test_init_retries_are_bounded_and_service_survives(self, storage, client):
        client.head_bucket.side_effect = _conn_error()

        await storage.initialize()  # must not raise

        assert client.head_bucket.call_count == s3_storage.INIT_MAX_ATTEMPTS
        assert not storage.is_ready

    async def test_init_does_not_retry_access_denied(self, storage, client):
        client.head_bucket.side_effect = _client_error("404", "HeadBucket")
        client.create_bucket.side_effect = _client_error("AccessDenied", "CreateBucket")

        await storage.initialize()

        assert client.create_bucket.call_count == 1
        assert not storage.is_ready


class TestEnsureReady:
    async def test_init_failed_then_session_ensure_creates_bucket(self, storage, client):
        client.head_bucket.side_effect = _conn_error()
        await storage.initialize()
        assert not storage.is_ready

        # MinIO is back, the bucket is missing
        client.head_bucket.side_effect = _client_error("NoSuchBucket", "HeadBucket")
        client.create_bucket.side_effect = None
        await storage.ensure_ready()

        client.create_bucket.assert_called_once_with(Bucket="sandbox-workspace")
        assert storage.is_ready

    async def test_ready_storage_is_not_rechecked(self, storage, client):
        client.head_bucket.return_value = {}
        await storage.initialize()
        client.head_bucket.reset_mock()

        await storage.ensure_ready()
        await storage.ensure_ready()

        client.head_bucket.assert_not_called()

    async def test_persistent_unreachable_gives_clear_error(self, storage, client):
        client.head_bucket.side_effect = _conn_error()

        with pytest.raises(StorageError) as exc:
            await storage.ensure_ready()

        assert "sandbox-workspace" in str(exc.value)
        assert "minio:9000" in str(exc.value)
        assert not storage.is_ready
        assert client.head_bucket.call_count == 1  # no retry storm on the session path

    async def test_access_denied_on_create_gives_clear_error(self, storage, client):
        client.head_bucket.side_effect = _client_error("NotFound", "HeadBucket")
        client.create_bucket.side_effect = _client_error("AccessDenied", "CreateBucket")

        with pytest.raises(StorageError) as exc:
            await storage.ensure_ready()

        assert "sandbox-workspace" in str(exc.value)
        assert "AccessDenied" in str(exc.value)

    @pytest.mark.parametrize("code", ["404", "NoSuchBucket", "NotFound"])
    async def test_missing_bucket_codes_all_create(self, storage, client, code):
        client.head_bucket.side_effect = _client_error(code, "HeadBucket")

        await storage.ensure_ready()

        client.create_bucket.assert_called_once()

    async def test_existing_bucket_is_untouched(self, storage, client):
        client.head_bucket.return_value = {}

        await storage.ensure_ready()

        client.create_bucket.assert_not_called()
        client.delete_bucket.assert_not_called()
        assert storage.is_ready

    async def test_concurrent_ensure_with_already_owned_succeeds(self, storage, client):
        client.head_bucket.side_effect = _client_error("NoSuchBucket", "HeadBucket")
        client.create_bucket.side_effect = _client_error("BucketAlreadyOwnedByYou", "CreateBucket")

        await asyncio.gather(*(storage.ensure_ready() for _ in range(5)))

        assert storage.is_ready

    async def test_bucket_already_exists_ours_succeeds(self, storage, client):
        # HEAD says missing, then create races with another creator, then HEAD succeeds
        client.head_bucket.side_effect = [_client_error("404", "HeadBucket"), {}]
        client.create_bucket.side_effect = _client_error("BucketAlreadyExists", "CreateBucket")

        await storage.ensure_ready()

        assert storage.is_ready

    async def test_bucket_already_exists_not_ours_fails(self, storage, client):
        client.head_bucket.side_effect = [
            _client_error("404", "HeadBucket"),
            _client_error("403", "HeadBucket"),
        ]
        client.create_bucket.side_effect = _client_error("BucketAlreadyExists", "CreateBucket")

        with pytest.raises(StorageError):
            await storage.ensure_ready()

    async def test_callers_queued_behind_a_failed_check_share_its_failure(self, storage, client):
        def unreachable(**_):
            time.sleep(0.05)  # keep the lock held while the other callers queue up
            raise _conn_error()

        client.head_bucket.side_effect = unreachable

        results = await asyncio.gather(
            *(storage.ensure_ready() for _ in range(5)), return_exceptions=True
        )

        assert all(isinstance(r, StorageError) for r in results)
        assert client.head_bucket.call_count == 1

        # A later session checks again instead of reusing the old failure
        client.head_bucket.side_effect = None
        client.head_bucket.return_value = {}
        await storage.ensure_ready()

        assert client.head_bucket.call_count == 2
        assert storage.is_ready


class TestReadyCheckClient:
    """The readiness check must not stack botocore retries on the startup retries"""

    @pytest.fixture
    def clients(self):
        settings = Mock()
        settings.s3_endpoint_url = "http://minio:9000"
        settings.s3_access_key_id = "k"
        settings.s3_secret_access_key = "s"
        settings.s3_region = "us-east-1"
        settings.s3_bucket = "sandbox-workspace"
        main, ready = Mock(), Mock()
        main.meta.region_name = ready.meta.region_name = "us-east-1"
        with patch.object(s3_storage.boto3, "client", side_effect=[main, ready]) as factory:
            with patch.object(s3_storage, "get_settings", return_value=settings):
                storage = S3Storage()
        return storage, main, ready, factory

    def test_ready_client_has_short_timeouts_and_no_internal_retries(self, clients):
        _, _, _, factory = clients
        main_call, ready_call = factory.call_args_list

        assert "config" not in main_call.kwargs  # file operations keep the defaults
        config = ready_call.kwargs["config"]
        assert config.connect_timeout == s3_storage.READY_CHECK_CONNECT_TIMEOUT_SECONDS
        assert config.read_timeout == s3_storage.READY_CHECK_READ_TIMEOUT_SECONDS
        assert config.retries == {"total_max_attempts": 1}
        assert ready_call.kwargs["endpoint_url"] == "http://minio:9000"

    async def test_initialize_and_ensure_ready_use_the_ready_client(self, clients):
        storage, main, ready, _ = clients
        ready.head_bucket.side_effect = _conn_error()

        await storage.initialize()
        with pytest.raises(StorageError):
            await storage.ensure_ready()

        assert ready.head_bucket.call_count == s3_storage.INIT_MAX_ATTEMPTS + 1
        main.head_bucket.assert_not_called()

    async def test_file_operations_keep_the_default_client(self, clients):
        storage, main, ready, _ = clients
        main.head_bucket.return_value = {}

        await storage.upload_file("sessions/s1/a.txt", b"x")

        main.head_bucket.assert_called_once_with(Bucket="sandbox-workspace")
        ready.head_bucket.assert_not_called()


class TestSessionCreationEnsuresStorage:
    @pytest.fixture
    def service(self):
        template_repo = Mock()
        template_repo.find_by_id = AsyncMock(return_value=None)
        scheduler = Mock()
        scheduler.schedule = AsyncMock()
        storage = Mock()
        storage.ensure_ready = AsyncMock(side_effect=StorageError("bucket 'b' is not ready"))
        svc = SessionService(
            session_repo=Mock(),
            execution_repo=Mock(),
            template_repo=template_repo,
            scheduler=scheduler,
            storage_service=storage,
        )
        return svc, template_repo, scheduler, storage

    async def test_storage_not_ready_fails_before_scheduling(self, service):
        svc, template_repo, scheduler, storage = service
        template_repo.find_by_id.return_value = Mock(image="python:3.11")

        with pytest.raises(StorageError):
            await svc.create_session(CreateSessionCommand(template_id="t1"))

        storage.ensure_ready.assert_awaited_once()
        scheduler.schedule.assert_not_called()


class TestCreateSessionRoute:
    async def test_storage_error_maps_to_503(self):
        from fastapi import HTTPException

        from src.interfaces.rest.api.v1 import sessions as route
        from src.interfaces.rest.schemas.request import CreateSessionRequest

        service = Mock()
        service.create_session = AsyncMock(side_effect=StorageError("bucket 'b' is not ready"))

        with pytest.raises(HTTPException) as exc:
            await route.create_session(CreateSessionRequest(template_id="t1"), service)

        assert exc.value.status_code == 503
        assert "bucket 'b'" in exc.value.detail


class TestMountScriptDiagnostics:
    def _assert_diagnostics(self, script: str):
        assert "NoSuchBucket" in script
        assert "s3_init_fail" in script
        assert "2>&1" in script  # s3fs output is captured, not swallowed
        fail_body = script[script.index("s3_init_fail() {") :]
        assert "fusermount -u /mnt/s3-init" in fail_body
        assert "exit 1" in fail_body

    def test_docker_script(self):
        sched = DockerScheduler.__new__(DockerScheduler)
        script = sched._build_s3_mount_entrypoint(
            s3_bucket="b",
            s3_prefix="sessions/s1",
            s3_endpoint_url="http://minio:9000",
            s3_access_key="k",
            s3_secret_key="s",
        )
        self._assert_diagnostics(script)

    def test_k8s_script(self):
        from src.infrastructure.container_scheduler.base import ContainerConfig

        sched = K8sScheduler(namespace="ns")
        config = ContainerConfig(
            image="python:3.11",
            name="s",
            cpu_limit="1",
            memory_limit="512Mi",
            disk_limit="1Gi",
            env_vars={},
            labels={},
            network_name="n",
            workspace_path="s3://b/sessions/s1/",
        )
        container = sched._build_executor_container(
            config=config, use_s3_mount=True, has_dependencies=False
        )
        self._assert_diagnostics(container.command[2])
