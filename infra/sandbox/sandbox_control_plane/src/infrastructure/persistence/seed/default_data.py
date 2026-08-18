"""
Default data definitions

The default runtime nodes and templates used across environments.
Keeping every default in one place makes them easier to maintain.
Column names carry the f_ prefix, following the table naming convention.
"""

from __future__ import annotations

import os
import time
from decimal import Decimal
from pathlib import Path
from typing import TYPE_CHECKING

from structlog import get_logger

if TYPE_CHECKING:
    from src.infrastructure.persistence.models.runtime_node_model import RuntimeNodeModel
    from src.infrastructure.persistence.models.template_model import TemplateModel

logger = get_logger(__name__)

DEFAULT_TEMPLATE_IMAGE_REGISTRY = "swr.cn-east-3.myhuaweicloud.com/kweaver-ai/dip"
DEFAULT_PYTHON_BASIC_TEMPLATE_IMAGE_REPOSITORY = "sandbox-template-python-basic"
DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY = "sandbox-template-multi-language"


def get_project_version() -> str:
    """
    Read the project version.

    PROJECT_VERSION and TEMPLATE_IMAGE_TAG win; without them the VERSION file at the repository root applies.
    """
    env_version = os.getenv("TEMPLATE_IMAGE_TAG") or os.getenv("PROJECT_VERSION")
    if env_version:
        return env_version.strip()

    version_candidates = [
        Path("/app/VERSION"),
        Path(__file__).resolve().parents[5] / "VERSION",
    ]
    for version_file in version_candidates:
        try:
            version = version_file.read_text(encoding="utf-8").strip()
        except OSError:
            continue
        if version:
            return version

    logger.warning(
        "VERSION file not found or empty, falling back to latest",
        paths=[str(path) for path in version_candidates],
    )
    return "latest"


def build_template_image_url(repository: str) -> str:
    """Build the default SWR template image URL from the project version."""
    registry = os.getenv("DEFAULT_TEMPLATE_IMAGE_REGISTRY") or DEFAULT_TEMPLATE_IMAGE_REGISTRY
    image_name = f"{registry.rstrip('/')}/{repository.lstrip('/')}" if registry else repository
    return f"{image_name}:{get_project_version()}"


def get_default_template_image_url() -> str:
    """
    Get the default template image URL

    Read from DEFAULT_TEMPLATE_IMAGE; without it, build the default SWR address from the VERSION file.

    Returns:
        The template image URL
    """
    repository = os.getenv(
        "DEFAULT_TEMPLATE_IMAGE_REPOSITORY",
        DEFAULT_PYTHON_BASIC_TEMPLATE_IMAGE_REPOSITORY,
    )
    image_url = os.getenv("DEFAULT_TEMPLATE_IMAGE") or build_template_image_url(repository)
    logger.info(
        "Getting default template image URL from environment", DEFAULT_TEMPLATE_IMAGE=image_url
    )
    return image_url


def get_multi_language_template_image_url() -> str:
    """
    Get the multi-language composite template image URL.

    Read from DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE; without it, build the default SWR address from the VERSION file.
    """
    image_url = os.getenv(
        "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE"
    ) or build_template_image_url(
        os.getenv(
            "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY",
            DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY,
        )
    )
    logger.info(
        "Getting multi-language template image URL from environment",
        DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE=image_url,
    )
    return image_url


def get_default_runtime_nodes() -> list[RuntimeNodeModel]:
    """
    Get the default runtime nodes

    Returns:
        The default runtime nodes
    """
    from src.infrastructure.persistence.models.runtime_node_model import RuntimeNodeModel

    now_ms = int(time.time() * 1000)
    return [
        RuntimeNodeModel(
            f_node_id="docker-local",
            f_hostname="sandbox-control-plane",
            f_runtime_type="docker",
            f_ip_address="127.0.0.1",
            f_api_endpoint="unix:///var/run/docker.sock",
            f_status="online",
            f_total_cpu_cores=Decimal("8.0"),
            f_total_memory_mb=16384,
            f_max_containers=50,
            f_running_containers=0,
            f_allocated_cpu_cores=Decimal("0"),
            f_allocated_memory_mb=0,
            f_cached_images="[]",
            f_labels='{"environment": "development", "type": "default"}',
            f_last_heartbeat_at=now_ms,
            # Audit columns
            f_created_at=now_ms,
            f_created_by="system",
            f_updated_at=now_ms,
            f_updated_by="system",
            f_deleted_at=0,
            f_deleted_by="",
        ),
    ]


def get_default_templates() -> list[TemplateModel]:
    """
    Get the default templates

    Returns:
        The default templates
    """
    import json

    from src.infrastructure.persistence.models.template_model import TemplateModel

    now_ms = int(time.time() * 1000)
    return [
        TemplateModel(
            f_id="python-basic",
            f_name="Python Basic",
            f_description="基础 Python 执行环境",
            f_image_url=get_default_template_image_url(),
            f_base_image="",
            f_runtime_type="python3.11",
            f_default_cpu_cores=Decimal("1.0"),
            f_default_memory_mb=512,
            f_default_disk_mb=1024,
            f_default_timeout_sec=300,
            f_is_active=1,
            f_pre_installed_packages="[]",
            f_default_env_vars="",
            f_security_context="",
            # Audit columns
            f_created_at=now_ms,
            f_created_by="system",
            f_updated_at=now_ms,
            f_updated_by="system",
            f_deleted_at=0,
            f_deleted_by="",
        ),
        TemplateModel(
            f_id="multi-language",
            f_name="Multi Language Basic",
            f_description="Python、Go、Bash 多语言复合执行环境",
            f_image_url=get_multi_language_template_image_url(),
            f_base_image="",
            f_runtime_type="multi",
            f_default_cpu_cores=Decimal("1.0"),
            f_default_memory_mb=512,
            f_default_disk_mb=1024,
            f_default_timeout_sec=300,
            f_is_active=1,
            f_pre_installed_packages=json.dumps(["python", "go", "bash"]),
            f_default_env_vars="",
            f_security_context="",
            # Audit columns
            f_created_at=now_ms,
            f_created_by="system",
            f_updated_at=now_ms,
            f_updated_by="system",
            f_deleted_at=0,
            f_deleted_by="",
        ),
    ]
