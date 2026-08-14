# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import importlib.util
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


if __name__ == "__main__":
    unittest.main()
