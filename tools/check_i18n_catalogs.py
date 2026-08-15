# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Validate Python i18n catalog shape without importing application modules."""

from __future__ import annotations

import ast
import json
import re
import string
import sys
from collections import Counter
from pathlib import Path
from typing import Any


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
PYTHON_CATALOGS = (
    (
        "mf-model-api",
        REPOSITORY_ROOT / "infra/mf-model-api/app/commons/i18n/zh_cn.py",
        REPOSITORY_ROOT / "infra/mf-model-api/app/commons/i18n/en_us.py",
    ),
    (
        "mf-model-manager",
        REPOSITORY_ROOT / "infra/mf-model-manager/app/commons/i18n/zh_cn.py",
        REPOSITORY_ROOT / "infra/mf-model-manager/app/commons/i18n/en_us.py",
    ),
)
JSON_CATALOGS = (
    (
        "context-loader",
        REPOSITORY_ROOT / "adp/context-loader/agent-retrieval/server/infra/localize/locales/zh-Hans.json",
        REPOSITORY_ROOT / "adp/context-loader/agent-retrieval/server/infra/localize/locales/en-US.json",
    ),
    (
        "execution-factory",
        REPOSITORY_ROOT / "adp/execution-factory/operator-integration/server/infra/localize/locales/zh-Hans.json",
        REPOSITORY_ROOT / "adp/execution-factory/operator-integration/server/infra/localize/locales/en-US.json",
    ),
)
MCP_SCHEMAS_DIRECTORY = REPOSITORY_ROOT / "adp/context-loader/agent-retrieval/server/driveradapters/mcp/schemas"

PERCENT_PLACEHOLDER = re.compile(r"(?<!%)%(?:[-+#0 ]*\d*(?:\.\d+)?[bcdeEfFgGosxXqTUv])")
GO_TEMPLATE_PLACEHOLDER = re.compile(r"{{\s*([^{}]+?)\s*}}")


def node_name(node: ast.AST) -> str:
    if isinstance(node, ast.Name):
        return node.id
    if isinstance(node, ast.Attribute):
        return f"{node_name(node.value)}.{node.attr}"
    return ast.unparse(node)


def string_dict(node: ast.AST, path: Path) -> dict[str, str]:
    if not isinstance(node, ast.Dict):
        raise ValueError(f"{path}: every error message must be a dict")

    result: dict[str, str] = {}
    for key, value in zip(node.keys, node.values, strict=True):
        if not isinstance(key, ast.Constant) or not isinstance(key.value, str):
            raise ValueError(f"{path}: error message fields must use string keys")
        if key.value == "code":
            result[key.value] = node_name(value)
            continue
        if not isinstance(value, ast.Constant) or not isinstance(value.value, str):
            raise ValueError(f"{path}: {key.value} must use a string value")
        result[key.value] = value.value
    return result


def load_catalog(path: Path) -> dict[str, dict[str, str]]:
    try:
        tree = ast.parse(path.read_text(encoding="utf-8"), filename=str(path))
    except (OSError, SyntaxError) as error:
        raise ValueError(f"{path}: cannot parse catalog: {error}") from error

    for statement in tree.body:
        if not isinstance(statement, ast.Assign):
            continue
        if not any(isinstance(target, ast.Name) and target.id == "error_messages" for target in statement.targets):
            continue
        if not isinstance(statement.value, ast.Dict):
            raise ValueError(f"{path}: error_messages must be a dict")

        result: dict[str, dict[str, str]] = {}
        for key, value in zip(statement.value.keys, statement.value.values, strict=True):
            if key is None:
                raise ValueError(f"{path}: error_messages cannot unpack entries")
            code = node_name(key)
            if code in result:
                raise ValueError(f"{path}: duplicate error code {code}")
            result[code] = string_dict(value, path)
        return result
    raise ValueError(f"{path}: error_messages was not found")


def placeholders(value: str) -> Counter[str]:
    fields: Counter[str] = Counter()
    try:
        parsed = string.Formatter().parse(value)
    except ValueError as error:
        raise ValueError(f"invalid format template {value!r}: {error}") from error
    for _, field_name, _, _ in parsed:
        if field_name is None:
            continue
        if field_name == "" or field_name.isdecimal():
            raise ValueError(f"positional placeholder is not allowed in {value!r}")
        fields[field_name] += 1
    fields.update(f"printf:{match.group(0)}" for match in PERCENT_PLACEHOLDER.finditer(value))
    fields.update(f"template:{match.group(1)}" for match in GO_TEMPLATE_PLACEHOLDER.finditer(value))
    return fields


def validate_text(path: str, value: str) -> str | None:
    if not value.strip():
        return f"{path} must not be empty"
    if "??" in value:
        return f"{path} contains an invalid placeholder value ??"
    return None


def load_json_catalog(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"{path}: cannot parse JSON catalog: {error}") from error


def flatten_json_strings(value: Any, path: str = "") -> dict[str, str]:
    if isinstance(value, str):
        return {path: value}
    if not isinstance(value, dict):
        raise ValueError(f"{path or '<root>'}: expected an object or string")
    result: dict[str, str] = {}
    for key, child in value.items():
        if not isinstance(key, str):
            raise ValueError(f"{path or '<root>'}: object keys must be strings")
        child_path = f"{path}.{key}" if path else key
        result.update(flatten_json_strings(child, child_path))
    return result


def validate_json_catalog_pair(name: str, baseline_path: Path, translated_path: Path) -> list[str]:
    baseline = flatten_json_strings(load_json_catalog(baseline_path))
    translated = flatten_json_strings(load_json_catalog(translated_path))
    errors: list[str] = []
    missing = sorted(set(baseline) - set(translated))
    unexpected = sorted(set(translated) - set(baseline))
    if missing:
        errors.append(f"{name}: {translated_path} is missing keys: {', '.join(missing)}")
    if unexpected:
        errors.append(f"{name}: {translated_path} has unexpected keys: {', '.join(unexpected)}")
    for catalog_path, catalog in ((baseline_path, baseline), (translated_path, translated)):
        for key, value in sorted(catalog.items()):
            if error := validate_text(f"{catalog_path}:{key}", value):
                errors.append(f"{name}: {error}")
    for key in sorted(set(baseline) & set(translated)):
        if not key.startswith("template."):
            try:
                expected, actual = placeholders(baseline[key]), placeholders(translated[key])
            except ValueError as error:
                errors.append(f"{name}: {key}: {error}")
                continue
            if expected != actual:
                errors.append(f"{name}: {key} placeholders differ: expected {dict(expected)}, got {dict(actual)}")
    return errors


def get_json_path(value: Any, path: str) -> Any:
    current = value
    for part in path.split("."):
        if not isinstance(current, dict) or part not in current:
            raise ValueError(f"path {path!r} does not exist")
        current = current[part]
    return current


def validate_mcp_locale_resources(schemas_directory: Path, locale: str = "en-US") -> list[str]:
    errors: list[str] = []
    locale_directory = schemas_directory / "locales" / locale
    try:
        base_tools = load_json_catalog(schemas_directory / "tools_meta.json")
        translated_tools = load_json_catalog(locale_directory / "tools_meta.json")
        instructions = (locale_directory / "instructions.txt").read_text(encoding="utf-8")
        descriptions = load_json_catalog(locale_directory / "schema_descriptions.json")
    except (OSError, ValueError) as error:
        return [f"mcp: {error}"]
    if not isinstance(base_tools, dict) or not isinstance(translated_tools, dict):
        return ["mcp: tools_meta.json must contain objects"]
    static_tools = {
        tool_key for tool_key in base_tools if (schemas_directory / f"{tool_key}.json").exists()
    }
    missing = sorted(static_tools - set(translated_tools))
    unexpected = sorted(set(translated_tools) - set(base_tools))
    if missing:
        errors.append(f"mcp: localized tools_meta.json is missing tools: {', '.join(missing)}")
    if unexpected:
        errors.append(f"mcp: localized tools_meta.json has unexpected tools: {', '.join(unexpected)}")
    for tool_key in sorted(static_tools & set(translated_tools)):
        base = base_tools[tool_key]
        if not isinstance(base, dict):
            errors.append(f"mcp: {tool_key} base metadata must be an object")
            continue
        localized = translated_tools[tool_key]
        if not isinstance(localized, dict):
            errors.append(f"mcp: {tool_key} localized metadata must be an object")
            continue
        if "name" in localized and localized["name"] != base.get("name"):
            errors.append(f"mcp: {tool_key}.name must not change the machine identifier")
        for field in ("title", "group_title", "description"):
            value = localized.get(field)
            if not isinstance(value, str):
                errors.append(f"mcp: {tool_key}.{field} must be a string")
            elif error := validate_text(f"{tool_key}.{field}", value):
                errors.append(f"mcp: {error}")
    if error := validate_text("instructions.txt", instructions):
        errors.append(f"mcp: {error}")
    if not isinstance(descriptions, dict):
        return errors + ["mcp: schema_descriptions.json must contain an object"]
    for tool_key, values in descriptions.items():
        if tool_key not in base_tools:
            errors.append(f"mcp: schema descriptions reference unknown tool {tool_key}")
            continue
        if not isinstance(values, dict):
            errors.append(f"mcp: schema descriptions for {tool_key} must be an object")
            continue
        try:
            schema = load_json_catalog(schemas_directory / f"{tool_key}.json")
        except ValueError as error:
            errors.append(f"mcp: {error}")
            continue
        for path, text in values.items():
            if not isinstance(text, str):
                errors.append(f"mcp: {tool_key}.{path} must be a string")
                continue
            if error := validate_text(f"{tool_key}.{path}", text):
                errors.append(f"mcp: {error}")
            try:
                original = get_json_path(schema, path)
            except ValueError as error:
                errors.append(f"mcp: {tool_key}: {error}")
            else:
                if not isinstance(original, str):
                    errors.append(f"mcp: {tool_key}.{path} must overlay a string field")
    return errors


def validate_catalog_pair(name: str, baseline_path: Path, translated_path: Path) -> list[str]:
    baseline = load_catalog(baseline_path)
    translated = load_catalog(translated_path)
    errors: list[str] = []

    missing_codes = sorted(set(baseline) - set(translated))
    unexpected_codes = sorted(set(translated) - set(baseline))
    if missing_codes:
        errors.append(f"{name}: {translated_path} is missing error codes: {', '.join(missing_codes)}")
    if unexpected_codes:
        errors.append(f"{name}: {translated_path} has unexpected error codes: {', '.join(unexpected_codes)}")

    for code in sorted(set(baseline) & set(translated)):
        baseline_fields = baseline[code]
        translated_fields = translated[code]
        missing_fields = sorted(set(baseline_fields) - set(translated_fields))
        unexpected_fields = sorted(set(translated_fields) - set(baseline_fields))
        if missing_fields:
            errors.append(f"{name}: {code} is missing fields in {translated_path}: {', '.join(missing_fields)}")
        if unexpected_fields:
            errors.append(f"{name}: {code} has unexpected fields in {translated_path}: {', '.join(unexpected_fields)}")

        for field in sorted(set(baseline_fields) & set(translated_fields)):
            try:
                expected = placeholders(baseline_fields[field])
                actual = placeholders(translated_fields[field])
            except ValueError as error:
                errors.append(f"{name}: {code}.{field}: {error}")
                continue
            if expected != actual:
                errors.append(
                    f"{name}: {code}.{field} placeholders differ: "
                    f"expected {dict(expected)}, got {dict(actual)}"
                )
    return errors


def main() -> int:
    errors: list[str] = []
    for catalog in PYTHON_CATALOGS:
        try:
            errors.extend(validate_catalog_pair(*catalog))
        except ValueError as error:
            errors.append(str(error))
    for catalog in JSON_CATALOGS:
        try:
            errors.extend(validate_json_catalog_pair(*catalog))
        except ValueError as error:
            errors.append(str(error))
    try:
        errors.extend(validate_mcp_locale_resources(MCP_SCHEMAS_DIRECTORY))
    except ValueError as error:
        errors.append(str(error))
    if errors:
        print("i18n catalog validation failed:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    print("i18n catalog validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
