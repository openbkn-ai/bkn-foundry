"""Unit tests for resource limit."""
import pytest

from src.domain.value_objects.resource_limit import ResourceLimit


class TestResourceLimit:
    """Tests for TestResourceLimit."""

    def test_create_default(self):
        """Test create default."""
        limit = ResourceLimit.default()

        assert limit.cpu == "1"
        assert limit.memory == "512Mi"
        assert limit.disk == "1Gi"
        assert limit.max_processes == 128

    def test_create_custom(self):
        """Test create custom."""
        limit = ResourceLimit(
            cpu="2",
            memory="1Gi",
            disk="10Gi",
            max_processes=256
        )

        assert limit.cpu == "2"
        assert limit.memory == "1Gi"
        assert limit.disk == "10Gi"
        assert limit.max_processes == 256

    def test_invalid_cpu(self):
        """Test invalid CPU."""
        with pytest.raises(ValueError, match="Invalid cpu format"):
            ResourceLimit(
                cpu="invalid",
                memory="512Mi",
                disk="1Gi"
            )

    def test_negative_cpu(self):
        """Test negative CPU."""
        with pytest.raises(ValueError, match="cpu must be positive"):
            ResourceLimit(
                cpu="-1",
                memory="512Mi",
                disk="1Gi"
            )

    def test_invalid_memory_format(self):
        """Test invalid memory format."""
        with pytest.raises(ValueError, match="Invalid memory format"):
            ResourceLimit(
                cpu="1",
                memory="invalid",
                disk="1Gi"
            )

    def test_invalid_disk_format(self):
        """Test invalid disk format."""
        with pytest.raises(ValueError, match="Invalid disk format"):
            ResourceLimit(
                cpu="1",
                memory="512Mi",
                disk="invalid"
            )

    def test_negative_max_processes(self):
        """Test negative max processes."""
        with pytest.raises(ValueError, match="max_processes must be positive"):
            ResourceLimit(
                cpu="1",
                memory="512Mi",
                disk="1Gi",
                max_processes=-1
            )

    def test_with_cpu(self):
        """Test with CPU."""
        limit = ResourceLimit.default()
        new_limit = limit.with_cpu("2")

        # Test setup.
        assert limit.cpu == "1"

        # Test setup.
        assert new_limit.cpu == "2"
        assert new_limit.memory == limit.memory

    def test_with_memory(self):
        """Test with memory."""
        limit = ResourceLimit.default()
        new_limit = limit.with_memory("1Gi")

        # Test setup.
        assert limit.memory == "512Mi"

        # Test setup.
        assert new_limit.memory == "1Gi"
        assert new_limit.cpu == limit.cpu

    def test_frozen(self):
        """Test frozen."""
        limit = ResourceLimit.default()

        with pytest.raises(Exception):  # Test setup.
            limit.cpu = "2"

    def test_valid_size_formats(self):
        """Test valid size formats."""
        valid_formats = [
            ("512Mi", True),
            ("1Gi", True),
            ("10Gi", True),
            ("100Mi", True),
            ("invalid", False),
            ("100", False),
            ("", False),
        ]

        for size, should_be_valid in valid_formats:
            if should_be_valid:
                ResourceLimit(
                    cpu="1",
                    memory=size,
                    disk="1Gi"
                )
            else:
                with pytest.raises(ValueError):
                    ResourceLimit(
                        cpu="1",
                        memory=size,
                        disk="1Gi"
                    )
