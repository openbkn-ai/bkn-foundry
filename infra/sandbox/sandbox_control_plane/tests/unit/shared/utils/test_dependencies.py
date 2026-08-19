"""Unit tests for dependencies."""
import pytest

from src.shared.utils.dependencies import (
    parse_dependencies_to_pip_specs,
    format_dependencies_for_script,
    build_dependency_install_script,
    format_dependency_install_script_for_shell,
)


class TestParseDependenciesToPipSpecs:
    """Tests for TestParseDependenciesToPipSpecs."""

    def test_parse_empty_list(self):
        """Test parse empty list."""
        result = parse_dependencies_to_pip_specs([])
        assert result == []

    def test_parse_none(self):
        """Test parse none."""
        result = parse_dependencies_to_pip_specs(None)
        assert result == []

    def test_parse_string_dependencies(self):
        """Test parse string dependencies."""
        deps = ["requests==2.31.0", "pandas>=2.0"]
        result = parse_dependencies_to_pip_specs(deps)

        assert result == ["requests==2.31.0", "pandas>=2.0"]

    def test_parse_dict_dependencies_with_version(self):
        """Test parse dict dependencies with version."""
        deps = [
            {"name": "requests", "version": "==2.31.0"},
            {"name": "pandas", "version": ">=2.0"}
        ]
        result = parse_dependencies_to_pip_specs(deps)

        assert result == ["requests==2.31.0", "pandas>=2.0"]

    def test_parse_dict_dependencies_without_version(self):
        """Test parse dict dependencies without version."""
        deps = [
            {"name": "requests"},
            {"name": "pandas", "version": ""}
        ]
        result = parse_dependencies_to_pip_specs(deps)

        assert result == ["requests", "pandas"]

    def test_parse_mixed_dependencies(self):
        """Test parse mixed dependencies."""
        deps = [
            "requests==2.31.0",
            {"name": "pandas", "version": ">=2.0"},
            "numpy"
        ]
        result = parse_dependencies_to_pip_specs(deps)

        assert result == ["requests==2.31.0", "pandas>=2.0", "numpy"]

    def test_parse_complex_version_specs(self):
        """Test parse complex version specs."""
        deps = [
            {"name": "requests", "version": ">=2.28.0,<3.0"},
            {"name": "django", "version": "==4.2.*"}
        ]
        result = parse_dependencies_to_pip_specs(deps)

        assert "requests>=2.28.0,<3.0" in result
        assert "django==4.2.*" in result


class TestFormatDependenciesForScript:
    """Tests for TestFormatDependenciesForScript."""

    def test_format_empty_list(self):
        """Test format empty list."""
        deps_json, deps_list = format_dependencies_for_script([])

        assert deps_json == ""
        assert deps_list == ""

    def test_format_none(self):
        """Test format none."""
        deps_json, deps_list = format_dependencies_for_script(None)

        assert deps_json == ""
        assert deps_list == ""

    def test_format_string_dependencies(self):
        """Test format string dependencies."""
        deps = ["requests==2.31.0", "pandas>=2.0"]
        deps_json, deps_list = format_dependencies_for_script(deps)

        assert "requests" in deps_json
        assert "pandas" in deps_json
        assert '"requests==2.31.0"' in deps_list
        assert '"pandas>=2.0"' in deps_list

    def test_format_dict_dependencies(self):
        """Test format dict dependencies."""
        deps = [{"name": "requests", "version": "==2.31.0"}]
        deps_json, deps_list = format_dependencies_for_script(deps)

        assert '"name"' in deps_json
        assert '"requests"' in deps_json
        assert '"requests==2.31.0"' in deps_list


class TestBuildDependencyInstallScript:
    """Tests for TestBuildDependencyInstallScript."""

    def test_build_script(self):
        """Test build script."""
        script = build_dependency_install_script()

        assert "pip3 install" in script
        assert "/opt/sandbox-venv" in script
        assert "pypi.org" in script


class TestFormatDependencyInstallScriptForShell:
    """Tests for TestFormatDependencyInstallScriptForShell."""

    def test_format_empty_list(self):
        """Test format empty list."""
        result = format_dependency_install_script_for_shell([])

        assert result == ""

    def test_format_none(self):
        """Test format none."""
        result = format_dependency_install_script_for_shell(None)

        assert result == ""

    def test_format_dependencies(self):
        """Test format dependencies."""
        deps = [
            {"name": "requests", "version": "==2.31.0"},
            "pandas>=2.0"
        ]
        result = format_dependency_install_script_for_shell(deps)

        assert "📦 Installing dependencies" in result
        assert "pip3 install" in result
        assert "/opt/sandbox-venv" in result

    def test_format_includes_pip_specs(self):
        """Test format includes pip specs."""
        deps = ["requests==2.31.0"]
        result = format_dependency_install_script_for_shell(deps)

        assert "requests==2.31.0" in result

    def test_format_includes_success_message(self):
        """Test format includes success message."""
        deps = ["requests"]
        result = format_dependency_install_script_for_shell(deps)

        assert "✅" in result
        assert "❌" in result
