"""Unit tests for template DTO."""
import pytest
from datetime import datetime

from src.application.dtos.template_dto import TemplateDTO
from src.domain.entities.template import Template
from src.domain.value_objects.resource_limit import ResourceLimit


class TestTemplateDTO:
    """Tests for TestTemplateDTO."""

    def test_create_with_required_fields(self):
        """Test create with required fields."""
        dto = TemplateDTO(
            id="python-test",
            name="Python Test",
            image_url="python:3.11",
            runtime_type="python3.11",
            default_cpu_cores=1.0,
            default_memory_mb=512,
            default_disk_mb=1024,
            default_timeout_sec=300
        )

        assert dto.id == "python-test"
        assert dto.name == "Python Test"
        assert dto.image_url == "python:3.11"
        assert dto.runtime_type == "python3.11"
        assert dto.default_cpu_cores == 1.0
        assert dto.default_memory_mb == 512
        assert dto.default_disk_mb == 1024
        assert dto.default_timeout_sec == 300
        assert dto.default_env_vars is None
        assert dto.is_active is True

    def test_create_with_all_fields(self):
        """Test create with all fields."""
        now = datetime.now()
        dto = TemplateDTO(
            id="python-test",
            name="Python Test",
            image_url="python:3.11",
            runtime_type="python3.11",
            default_cpu_cores=1.0,
            default_memory_mb=512,
            default_disk_mb=1024,
            default_timeout_sec=300,
            default_env_vars={"DEBUG": "true"},
            is_active=False,
            created_at=now,
            updated_at=now
        )

        assert dto.default_env_vars == {"DEBUG": "true"}
        assert dto.is_active is False
        assert dto.created_at == now
        assert dto.updated_at == now

    def test_from_entity(self):
        """Test from entity."""
        template = Template(
            id="python-test",
            name="Python Test",
            image="python:3.11",
            base_image="python:3.11-slim",
            default_resources=ResourceLimit(
                cpu="1",
                memory="512Mi",
                disk="1Gi"
            ),
            default_timeout_sec=300
        )

        dto = TemplateDTO.from_entity(template)

        assert dto.id == "python-test"
        assert dto.name == "Python Test"
        assert dto.image_url == "python:3.11"
        assert dto.default_cpu_cores == 1.0
        assert dto.default_memory_mb == 512
        assert dto.default_disk_mb == 1024
        assert dto.default_timeout_sec == 300

    def test_from_entity_with_gi_memory(self):
        """Test from entity with gi memory."""
        template = Template(
            id="python-test",
            name="Python Test",
            image="python:3.11",
            base_image="python:3.11-slim",
            default_resources=ResourceLimit(
                cpu="2",
                memory="2Gi",
                disk="10Gi"
            )
        )

        dto = TemplateDTO.from_entity(template)

        assert dto.default_memory_mb == 2048  # 2Gi = 2048MB
        assert dto.default_disk_mb == 10240  # 10Gi = 10240MB

    def test_from_entity_with_large_resources(self):
        """Test from entity with large resources."""
        template = Template(
            id="python-test",
            name="Python Test",
            image="python:3.11",
            base_image="python:3.11-slim",
            default_resources=ResourceLimit(
                cpu="4",
                memory="8Gi",
                disk="50Gi"
            )
        )

        dto = TemplateDTO.from_entity(template)

        assert dto.default_cpu_cores == 4.0
        assert dto.default_memory_mb == 8192  # 8Gi = 8192MB
        assert dto.default_disk_mb == 51200  # 50Gi = 51200MB

    def test_from_entity_with_default_resources(self):
        """Test from entity with default resources."""
        template = Template(
            id="python-test",
            name="Python Test",
            image="python:3.11",
            base_image="python:3.11-slim",
            default_resources=ResourceLimit.default()
        )

        dto = TemplateDTO.from_entity(template)

        assert dto.default_cpu_cores == 1.0
        assert dto.default_memory_mb == 512
        assert dto.default_disk_mb == 1024

    def test_to_dict(self):
        """Test to dict."""
        now = datetime.now()
        dto = TemplateDTO(
            id="python-test",
            name="Python Test",
            image_url="python:3.11",
            runtime_type="python3.11",
            default_cpu_cores=1.0,
            default_memory_mb=512,
            default_disk_mb=1024,
            default_timeout_sec=300,
            default_env_vars={"DEBUG": "true"},
            is_active=True,
            created_at=now,
            updated_at=now
        )

        result = dto.to_dict()

        assert result["id"] == "python-test"
        assert result["name"] == "Python Test"
        assert result["image_url"] == "python:3.11"
        assert result["runtime_type"] == "python3.11"
        assert result["default_cpu_cores"] == 1.0
        assert result["default_memory_mb"] == 512
        assert result["default_disk_mb"] == 1024
        assert result["default_timeout_sec"] == 300
        assert result["default_env_vars"] == {"DEBUG": "true"}
        assert result["is_active"] is True
        assert result["created_at"] == now.isoformat()
        assert result["updated_at"] == now.isoformat()

    def test_to_dict_without_dates(self):
        """Test to dict without dates."""
        dto = TemplateDTO(
            id="python-test",
            name="Python Test",
            image_url="python:3.11",
            runtime_type="python3.11",
            default_cpu_cores=1.0,
            default_memory_mb=512,
            default_disk_mb=1024,
            default_timeout_sec=300
        )

        result = dto.to_dict()

        assert result["created_at"] is None
        assert result["updated_at"] is None

    def test_is_dataclass(self):
        """Test is dataclass."""
        from dataclasses import is_dataclass

        assert is_dataclass(TemplateDTO)
