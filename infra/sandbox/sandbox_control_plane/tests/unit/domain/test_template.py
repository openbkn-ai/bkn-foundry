"""Unit tests for template."""
import pytest
from datetime import datetime

from src.domain.entities.template import Template
from src.domain.value_objects.resource_limit import ResourceLimit


class TestTemplate:
    """Tests for TestTemplate."""

    def test_create_template_success(self):
        """Test create template success."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim"
        )

        assert template.id == "python-datascience"
        assert template.name == "Python Data Science"
        assert template.image == "python:3.11-datascience"
        assert template.base_image == "python:3.11-slim"

    def test_create_template_with_default_resources(self):
        """Test create template with default resources."""
        template = Template(
            id="python-basic",
            name="Python Basic",
            image="python:3.11",
            base_image="python:3.11-slim"
        )

        assert template.default_resources.cpu == "1"
        assert template.default_resources.memory == "512Mi"
        assert template.default_resources.disk == "1Gi"

    def test_create_template_with_custom_resources(self):
        """Test create template with custom resources."""
        resources = ResourceLimit(
            cpu="2",
            memory="1Gi",
            disk="10Gi",
            max_processes=256
        )
        template = Template(
            id="python-high-memory",
            name="Python High Memory",
            image="python:3.11",
            base_image="python:3.11-slim",
            default_resources=resources
        )

        assert template.default_resources.cpu == "2"
        assert template.default_resources.memory == "1Gi"
        assert template.default_resources.disk == "10Gi"
        assert template.default_resources.max_processes == 256

    def test_create_template_invalid_name(self):
        """Test create template invalid name."""
        with pytest.raises(ValueError, match="name cannot be empty"):
            Template(
                id="test",
                name="",
                image="python:3.11",
                base_image="python:3.11-slim"
            )

    def test_create_template_invalid_image(self):
        """Test create template invalid image."""
        with pytest.raises(ValueError, match="image cannot be empty"):
            Template(
                id="test",
                name="Test",
                image="",
                base_image="python:3.11-slim"
            )

    def test_create_template_invalid_base_image(self):
        """Test create template invalid base image."""
        with pytest.raises(ValueError, match="base_image cannot be empty"):
            Template(
                id="test",
                name="Test",
                image="python:3.11",
                base_image=""
            )

    def test_update_image(self):
        """Test update image."""
        template = Template(
            id="python-basic",
            name="Python Basic",
            image="python:3.11",
            base_image="python:3.11-slim"
        )

        original_updated_at = template.updated_at
        template.update_image("python:3.12")

        assert template.image == "python:3.12"
        assert template.updated_at > original_updated_at

    def test_update_image_invalid(self):
        """Test update image invalid."""
        template = Template(
            id="python-basic",
            name="Python Basic",
            image="python:3.11",
            base_image="python:3.11-slim"
        )

        with pytest.raises(ValueError, match="image cannot be empty"):
            template.update_image("")

    def test_add_package(self):
        """Test add package."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim",
            pre_installed_packages=["numpy"]
        )

        template.add_package("pandas")

        assert "pandas" in template.pre_installed_packages
        assert "numpy" in template.pre_installed_packages

    def test_add_duplicate_package(self):
        """Test add duplicate package."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim",
            pre_installed_packages=["numpy"]
        )

        original_updated_at = template.updated_at
        template.add_package("numpy")  # Add an existing package.

        # Verify expected behavior.
        assert template.pre_installed_packages.count("numpy") == 1
        assert template.updated_at == original_updated_at

    def test_remove_package(self):
        """Test remove package."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim",
            pre_installed_packages=["numpy", "pandas"]
        )

        template.remove_package("pandas")

        assert "numpy" in template.pre_installed_packages
        assert "pandas" not in template.pre_installed_packages

    def test_remove_nonexistent_package(self):
        """Test remove nonexistent package."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim",
            pre_installed_packages=["numpy"]
        )

        original_updated_at = template.updated_at
        template.remove_package("pandas")  # Remove a nonexistent package.

        # Verify expected behavior.
        assert template.updated_at == original_updated_at

    def test_update_default_resources(self):
        """Test update default resources."""
        template = Template(
            id="python-basic",
            name="Python Basic",
            image="python:3.11",
            base_image="python:3.11-slim"
        )

        original_updated_at = template.updated_at
        new_resources = ResourceLimit(
            cpu="4",
            memory="2Gi",
            disk="20Gi",
            max_processes=512
        )
        template.update_default_resources(new_resources)

        assert template.default_resources.cpu == "4"
        assert template.default_resources.memory == "2Gi"
        assert template.default_resources.disk == "20Gi"
        assert template.default_resources.max_processes == 512
        assert template.updated_at > original_updated_at

    def test_has_package(self):
        """Test has package."""
        template = Template(
            id="python-datascience",
            name="Python Data Science",
            image="python:3.11-datascience",
            base_image="python:3.11-slim",
            pre_installed_packages=["numpy", "pandas"]
        )

        assert template.has_package("numpy") is True
        assert template.has_package("pandas") is True
        assert template.has_package("scikit-learn") is False

    def test_get_image_name(self):
        """Test get image name."""
        template1 = Template(
            id="python-basic",
            name="Python Basic",
            image="python:3.11",
            base_image="python:3.11-slim"
        )
        assert template1.get_image_name() == "python"

        template2 = Template(
            id="python-datascience",
            name="Python Data Science",
            image="sandbox-registry.example.com/python:3.11-datascience",
            base_image="python:3.11-slim"
        )
        assert template2.get_image_name() == "sandbox-registry.example.com/python"

        template3 = Template(
            id="python-latest",
            name="Python Latest",
            image="python:latest",
            base_image="python:3.11-slim"
        )
        assert template3.get_image_name() == "python"

    def test_security_context(self):
        """Test security context."""
        security_context = {
            "readonly_rootfs": True,
            "capabilities": ["CAP_NET_RAW"],
            "seccomp_profile": "strict"
        }
        template = Template(
            id="python-secure",
            name="Python Secure",
            image="python:3.11",
            base_image="python:3.11-slim",
            security_context=security_context
        )

        assert template.security_context == security_context
