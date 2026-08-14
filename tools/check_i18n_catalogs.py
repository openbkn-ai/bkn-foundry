# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Validate Python i18n catalog shape without importing application modules."""

from __future__ import annotations

import ast
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
        if field_name:
            fields[field_name] += 1
    return fields


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
        errors.extend(validate_catalog_pair(*catalog))
    if errors:
        print("i18n catalog validation failed:", file=sys.stderr)
        print("\n".join(f"- {error}" for error in errors), file=sys.stderr)
        return 1
    print("i18n catalog validation passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
