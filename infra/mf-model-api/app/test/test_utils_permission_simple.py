"""Tests for test_utils_permission_simple."""
import pytest
from unittest.mock import AsyncMock, Mock, patch
from app.utils.permission_manager import PermissionManager, permission_manager


class TestPermissionManagerSimple:
    """Tests for test permission manager simple."""

    def test_singleton_instance(self):
        """Test test singleton instance."""
        assert permission_manager is not None
        assert isinstance(permission_manager, PermissionManager)

    def test_instance_creation(self):
        """Test test instance creation."""
        manager = PermissionManager()
        assert manager is not None

    def test_has_base_url(self):
        """Test test has base url."""
        manager = PermissionManager()
        assert hasattr(manager, 'base_url')
        assert isinstance(manager.base_url, str)

    def test_has_auth_url(self):
        """Test test has auth url."""
        manager = PermissionManager()
        assert hasattr(manager, 'auth_url')
        assert isinstance(manager.auth_url, str)

    def test_has_session_attribute(self):
        """Test test has session attribute."""
        manager = PermissionManager()
        assert hasattr(manager, 'session')

    @pytest.mark.asyncio
    async def test_get_session_method_exists(self):
        """Test test get session method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'get_session')
        assert callable(manager.get_session)

    @pytest.mark.asyncio
    async def test_add_permission_method_exists(self):
        """Test test add permission method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'add_permission')
        assert callable(manager.add_permission)

    @pytest.mark.asyncio
    async def test_check_single_permission_method_exists(self):
        """Test test check single permission method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'check_single_permission')
        assert callable(manager.check_single_permission)

    @pytest.mark.asyncio
    async def test_get_permission_ids_method_exists(self):
        """Test test get permission ids method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'get_permission_ids')
        assert callable(manager.get_permission_ids)

    @pytest.mark.asyncio
    async def test_delete_permission_method_exists(self):
        """Test test delete permission method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'delete_permission')
        assert callable(manager.delete_permission)

    @pytest.mark.asyncio
    async def test_close_method_exists(self):
        """Test test close method exists."""
        manager = PermissionManager()
        assert hasattr(manager, 'close')
        assert callable(manager.close)

    @pytest.mark.asyncio
    async def test_add_permission_admin_user_shortcut(self):
        """Test test add permission admin user shortcut."""
        manager = PermissionManager()
        result = await manager.add_permission(
            user_id="266c6a42-6131-4d62-8f39-853e7093701c",
            resource_id="test",
            resource_name="test",
            resource_type="test",
            user_name="admin",
            role="admin"
        )
        assert result is True

