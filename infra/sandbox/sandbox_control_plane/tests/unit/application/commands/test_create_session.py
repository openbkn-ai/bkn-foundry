"""Unit tests for create session."""

import pytest

from src.application.commands.create_session import CreateSessionCommand


class TestCreateSessionCommandValidation:
    @pytest.mark.parametrize(
        "bad_id",
        [
            "foo; rm -rf /",
            "$(id)",
            "a`id`b",
            "../escape",
            "sess/../../etc",
            "a/b",
            "with space",
            "a|b",
            "a&b",
        ],
    )
    def test_rejects_unsafe_id(self, bad_id):
        # Test setup.
        # Test setup.
        with pytest.raises(ValueError):
            CreateSessionCommand(id=bad_id)
        with pytest.raises(ValueError):
            CreateSessionCommand(template_id=bad_id)

    def test_accepts_safe_id_and_template_id(self):
        cmd = CreateSessionCommand(id="sess_aoi_0", template_id="python-basic")
        assert cmd.id == "sess_aoi_0"
        assert cmd.template_id == "python-basic"

    def test_allows_none(self):
        # Verify expected behavior.
        cmd = CreateSessionCommand()
        assert cmd.id is None
        assert cmd.template_id is None
