"""Unit tests for default data."""

from src.infrastructure.persistence.seed.default_data import (
    DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY,
    DEFAULT_PYTHON_BASIC_TEMPLATE_IMAGE_REPOSITORY,
    DEFAULT_TEMPLATE_IMAGE_REGISTRY,
    get_default_templates,
    get_project_version,
)


def test_default_template_images_use_swr_registry(monkeypatch):
    """Test default template images use swr registry."""
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE_REGISTRY", raising=False)
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE_REPOSITORY", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY", raising=False)
    monkeypatch.delenv("PROJECT_VERSION", raising=False)
    monkeypatch.delenv("TEMPLATE_IMAGE_TAG", raising=False)

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}
    project_version = get_project_version()

    assert template_map["python-basic"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/"
        f"{DEFAULT_PYTHON_BASIC_TEMPLATE_IMAGE_REPOSITORY}:{project_version}"
    )
    assert template_map["multi-language"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/"
        f"{DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY}:{project_version}"
    )


def test_default_template_images_can_use_template_image_tag(monkeypatch):
    """Test default template images can use template image tag."""
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE_REGISTRY", raising=False)
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE_REPOSITORY", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY", raising=False)
    monkeypatch.setenv("TEMPLATE_IMAGE_TAG", "9.9.9")

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}

    assert template_map["python-basic"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/"
        f"{DEFAULT_PYTHON_BASIC_TEMPLATE_IMAGE_REPOSITORY}:9.9.9"
    )
    assert template_map["multi-language"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/"
        f"{DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY}:9.9.9"
    )


def test_default_template_images_can_be_overridden_independently(monkeypatch):
    """Test default template images can be overridden independently."""
    monkeypatch.setenv("DEFAULT_TEMPLATE_IMAGE", "registry.local/python-basic:dev")
    monkeypatch.setenv(
        "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE",
        "registry.local/multi-language:dev",
    )

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}

    assert template_map["python-basic"].f_image_url == "registry.local/python-basic:dev"
    assert template_map["multi-language"].f_image_url == "registry.local/multi-language:dev"


def test_default_template_images_can_use_registry_and_repositories(monkeypatch):
    """Test default template images can use registry and repositories."""
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE", raising=False)
    monkeypatch.setenv("DEFAULT_TEMPLATE_IMAGE_REGISTRY", "registry.local/team")
    monkeypatch.setenv("DEFAULT_TEMPLATE_IMAGE_REPOSITORY", "python-template")
    monkeypatch.setenv(
        "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY",
        "runtime/multi-template",
    )
    monkeypatch.setenv("TEMPLATE_IMAGE_TAG", "branch-tag")

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}

    assert template_map["python-basic"].f_image_url == "registry.local/team/python-template:branch-tag"
    assert (
        template_map["multi-language"].f_image_url
        == "registry.local/team/runtime/multi-template:branch-tag"
    )


def test_default_template_images_use_swr_when_registry_is_empty(monkeypatch):
    """Test default template images use swr when registry is empty."""
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE", raising=False)
    monkeypatch.delenv("DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE", raising=False)
    monkeypatch.setenv("DEFAULT_TEMPLATE_IMAGE_REGISTRY", "")
    monkeypatch.setenv("DEFAULT_TEMPLATE_IMAGE_REPOSITORY", "sandbox-template-python-basic")
    monkeypatch.setenv(
        "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE_REPOSITORY",
        "sandbox-template-multi-language",
    )
    monkeypatch.setenv("TEMPLATE_IMAGE_TAG", "branch-tag")

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}

    assert template_map["python-basic"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/sandbox-template-python-basic:branch-tag"
    )
    assert template_map["multi-language"].f_image_url == (
        f"{DEFAULT_TEMPLATE_IMAGE_REGISTRY}/sandbox-template-multi-language:branch-tag"
    )


def test_default_templates_include_multi_language(monkeypatch):
    """Test default templates include multi language."""
    monkeypatch.delenv("DEFAULT_TEMPLATE_IMAGE", raising=False)
    monkeypatch.setenv(
        "DEFAULT_MULTI_LANGUAGE_TEMPLATE_IMAGE",
        "custom-multi-language:latest",
    )

    templates = get_default_templates()
    template_map = {template.f_id: template for template in templates}

    assert "python-basic" in template_map
    assert "multi-language" in template_map
    assert template_map["multi-language"].f_image_url == "custom-multi-language:latest"
    assert template_map["multi-language"].f_runtime_type == "multi"
    assert template_map["multi-language"].f_is_active == 1
