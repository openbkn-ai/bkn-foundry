"""
Dependency parsing helpers

Converts and parses Python dependency package formats.
"""

import json
import re
from typing import List, Optional, Union

DEFAULT_PYTHON_PACKAGE_INDEX_URL = "https://pypi.org/simple/"


def normalize_python_package_index_url(index_url: Optional[str]) -> str:
    """Normalize a Python package index URL."""
    if not index_url:
        return DEFAULT_PYTHON_PACKAGE_INDEX_URL
    return index_url.strip() or DEFAULT_PYTHON_PACKAGE_INDEX_URL


def parse_pip_spec(spec: Union[str, dict]) -> dict[str, Optional[str]]:
    """
    Parse a pip requirement specifier.

    Returns:
        {"name": "requests", "version": "==2.31.0"}
    """
    if isinstance(spec, dict):
        return {
            "name": spec.get("name", ""),
            "version": spec.get("version") or None,
        }

    match = re.match(r"^\s*([A-Za-z0-9._-]+)\s*(.*)\s*$", spec or "")
    if not match:
        return {"name": spec.strip(), "version": None}

    name = match.group(1)
    version = match.group(2).strip() or None
    return {"name": name, "version": version}


def merge_pip_specs(existing: List[str], incoming: List[str]) -> List[str]:
    """Merge pip specifiers by package name; a later entry wins."""
    merged: dict[str, str] = {}

    for spec in existing + incoming:
        parsed = parse_pip_spec(spec)
        if parsed["name"]:
            merged[parsed["name"].lower()] = spec

    return list(merged.values())


def parse_dependencies_to_pip_specs(dependencies: Optional[List[Union[str, dict]]]) -> List[str]:
    """
    Convert a dependency list into pip specifiers

    Args:
        dependencies: the dependencies, each a string or a dict
            - string form: "requests==2.31.0" or "requests"
            - dict form: {"name": "requests", "version": "==2.31.0"}

    Returns:
        The pip specifiers, such as ["requests==2.31.0", "pandas>=2.0"]
    """
    if not dependencies:
        return []

    pip_specs = []
    for dep in dependencies:
        if isinstance(dep, dict):
            name = dep.get("name", "")
            version = dep.get("version", "")
            pip_specs.append(f"{name}{version}" if version else name)
        elif isinstance(dep, str):
            pip_specs.append(dep)

    return pip_specs


def format_dependencies_for_script(
    dependencies: Optional[List[Union[str, dict]]],
) -> tuple[str, str]:
    """
    Format the dependency list for a shell script

    Args:
        dependencies: the dependencies

    Returns:
        A (deps_json, deps_list) tuple
        - deps_json: the dependency list as a JSON string
        - deps_list: space-separated pip specifiers, for the shell script
    """
    if not dependencies:
        return "", ""

    pip_specs = parse_dependencies_to_pip_specs(dependencies)
    deps_json = json.dumps(dependencies)
    deps_list = " ".join(f'"{spec}"' for spec in pip_specs)

    return deps_json, deps_list


def build_dependency_install_script() -> str:
    """
    Build the shared Python dependency install script fragment

    Returns:
        The shell script that installs the dependencies into /opt/sandbox-venv
    """
    return """
# ========== Install the Python dependencies ==========
echo "📦 Installing dependencies: {deps_json}"
echo "📦 Pip specs: {pip_specs}"

# Install into the container's local filesystem rather than the S3 mount point:
# the mount point is a network filesystem and is a poor pip target.
VENV_DIR="/opt/sandbox-venv"
mkdir -p $VENV_DIR
mkdir -p /tmp/pip-cache

echo "Installing dependencies to local filesystem: $VENV_DIR"

if pip3 install \\
    --target $VENV_DIR \\
    --cache-dir /tmp/pip-cache \\
    --no-cache-dir \\
    --no-warn-script-location \\
    --disable-pip-version-check \\
    --index-url https://pypi.org/simple/ \\
    {deps_list}; then
    echo "✅ Dependencies installed successfully to $VENV_DIR"
    # Hand ownership to the sandbox user; the install runs as root, before gosu drops privileges
    chown -R sandbox:sandbox $VENV_DIR
    # Clear the cache
    rm -rf /tmp/pip-cache
else
    echo "❌ Failed to install dependencies"
    exit 1
fi
"""


def format_dependency_install_script_for_shell(
    dependencies: Optional[List[Union[str, dict]]],
) -> str:
    """
    Format the dependency install script for shell execution

    Args:
        dependencies: the dependencies

    Returns:
        The shell script as a string
    """
    if not dependencies:
        return ""

    deps_json, deps_list = format_dependencies_for_script(dependencies)
    pip_specs_quoted = " ".join(f'"{spec}"' for spec in deps_list.split() if spec)

    return f"""
# ========== Install the Python dependencies ==========
echo "📦 Installing dependencies: {deps_json}"
echo "📦 Pip specs: {pip_specs_quoted}"

# Install into the container's local filesystem
VENV_DIR="/opt/sandbox-venv"
mkdir -p $VENV_DIR
mkdir -p /tmp/pip-cache

echo "Installing dependencies to: $VENV_DIR"

if pip3 install \\
    --target $VENV_DIR \\
    --cache-dir /tmp/pip-cache \\
    --no-cache-dir \\
    --no-warn-script-location \\
    --disable-pip-version-check \\
    --index-url https://pypi.org/simple/ \\
    {deps_list}; then
    echo "✅ Dependencies installed successfully"
    # Clear the cache
    rm -rf /tmp/pip-cache
else
    echo "❌ Failed to install dependencies"
    exit 1
fi
"""
