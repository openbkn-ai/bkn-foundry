"""Keep explicit executor REST origins ahead of deployment defaults."""

from types import SimpleNamespace
from unittest.mock import patch

import pytest

from src.infrastructure.container_scheduler.base import ContainerConfig
from src.infrastructure.container_scheduler.k8s_scheduler import K8sScheduler


@pytest.mark.parametrize("explicit", [None, "http://explicit:80", ""])
def test_executor_rest_gateway_is_not_overwritten_or_duplicated(explicit):
    environment = {} if explicit is None else {"BKN_BASE_URL": explicit}
    config = ContainerConfig(
        image="python:3.11",
        name="gateway-test",
        env_vars=environment,
        cpu_limit="1",
        memory_limit="512Mi",
        disk_limit="1Gi",
        workspace_path="/workspace",
        labels={},
    )
    settings = SimpleNamespace(
        bkn_sandbox_mcp_url="http://retrieval:30780/mcp/", executor_image_pull_policy="IfNotPresent"
    )
    module = "src.infrastructure.container_scheduler.k8s_scheduler"
    with (
        patch(module + ".get_settings", return_value=settings),
        patch(module + ".resolve_bkn_base_url", return_value="http://gateway:80"),
    ):
        container = K8sScheduler(namespace="sandbox")._build_executor_container(
            config, use_s3_mount=False, has_dependencies=False
        )
    bases = [item.value for item in container.env if item.name == "BKN_BASE_URL"]
    assert bases == ["http://gateway:80" if explicit is None else explicit]
