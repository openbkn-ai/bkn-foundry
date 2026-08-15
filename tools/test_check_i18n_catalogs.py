# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("check_i18n_catalogs.py")
SPEC = importlib.util.spec_from_file_location("check_i18n_catalogs", SCRIPT_PATH)
assert SPEC and SPEC.loader
catalog_check = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(catalog_check)


class TestPythonCatalogValidation(unittest.TestCase):
    def test_rejects_positional_placeholders(self) -> None:
        with self.assertRaisesRegex(ValueError, "positional placeholder"):
            catalog_check.placeholders("Missing parameter: {}")
        with self.assertRaisesRegex(ValueError, "positional placeholder"):
            catalog_check.placeholders("Missing parameter: {0}")

    def test_reports_missing_codes_and_template_mismatches(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            directory = Path(temporary_directory)
            baseline = directory / "zh_cn.py"
            translated = directory / "en_us.py"
            baseline.write_text(
                "error_messages = {\n"
                "    ErrorCode.Required: {\n"
                "        'code': ErrorCode.Required,\n"
                "        'description': '缺少参数：{parameter}',\n"
                "    },\n"
                "    ErrorCode.Invalid: {\n"
                "        'code': ErrorCode.Invalid,\n"
                "        'description': '参数无效',\n"
                "    },\n"
                "}\n",
                encoding="utf-8",
            )
            translated.write_text(
                "error_messages = {\n"
                "    ErrorCode.Required: {\n"
                "        'code': ErrorCode.Required,\n"
                "        'description': 'Required parameter: {name}',\n"
                "    },\n"
                "}\n",
                encoding="utf-8",
            )

            errors = catalog_check.validate_catalog_pair("test-service", baseline, translated)

        self.assertTrue(any("missing error codes: ErrorCode.Invalid" in error for error in errors))
        self.assertTrue(any("placeholders differ" in error for error in errors))

    def test_json_catalog_rejects_recursive_key_and_placeholder_errors(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            directory = Path(temporary_directory)
            baseline = directory / "zh-Hans.json"
            translated = directory / "en-US.json"
            baseline.write_text(json.dumps({"nested": {"message": "参数 %s", "other": "有效"}}), encoding="utf-8")
            translated.write_text(json.dumps({"nested": {"message": "Parameter %d"}, "unexpected": "??"}), encoding="utf-8")

            errors = catalog_check.validate_json_catalog_pair("test-json", baseline, translated)

        self.assertTrue(any("missing keys: nested.other" in error for error in errors))
        self.assertTrue(any("unexpected keys: unexpected" in error for error in errors))
        self.assertTrue(any("placeholders differ" in error for error in errors))
        self.assertTrue(any("invalid placeholder value ??" in error for error in errors))

    def test_mcp_catalog_rejects_machine_identifier_and_invalid_overlays(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory)
            locale = schemas / "locales" / "en-US"
            locale.mkdir(parents=True)
            (schemas / "tools_meta.json").write_text(
                json.dumps({"example": {"name": "example", "title": "示例", "group_title": "分组", "description": "描述"}}),
                encoding="utf-8",
            )
            (schemas / "example.json").write_text(
                json.dumps({"input_schema": {"properties": {"field": {"description": "说明"}}}}),
                encoding="utf-8",
            )
            (locale / "tools_meta.json").write_text(
                json.dumps({"example": {"name": "renamed", "title": "Example", "group_title": "Examples", "description": "Description"}}),
                encoding="utf-8",
            )
            (locale / "schema_descriptions.json").write_text(
                json.dumps({"example": {"input_schema.properties.missing.description": "??"}}),
                encoding="utf-8",
            )
            (locale / "instructions.txt").write_text("", encoding="utf-8")

            errors = catalog_check.validate_mcp_locale_resources(schemas)

        self.assertTrue(any("must not change the machine identifier" in error for error in errors))
        self.assertTrue(any("instructions.txt must not be empty" in error for error in errors))
        self.assertTrue(any("does not exist" in error for error in errors))

    def test_mcp_catalog_reports_non_object_base_metadata(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory)
            locale = schemas / "locales" / "en-US"
            locale.mkdir(parents=True)
            (schemas / "tools_meta.json").write_text(json.dumps({"example": "invalid"}), encoding="utf-8")
            (schemas / "example.json").write_text(json.dumps({}), encoding="utf-8")
            (locale / "tools_meta.json").write_text(
                json.dumps({"example": {"title": "Example", "group_title": "Examples", "description": "Description"}}),
                encoding="utf-8",
            )
            (locale / "schema_descriptions.json").write_text(json.dumps({}), encoding="utf-8")
            (locale / "instructions.txt").write_text("Instructions", encoding="utf-8")

            errors = catalog_check.validate_mcp_locale_resources(schemas)

        self.assertEqual(errors, ["mcp: example base metadata must be an object"])


if __name__ == "__main__":
    unittest.main()
