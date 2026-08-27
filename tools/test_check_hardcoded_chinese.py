# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

import importlib.util
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path


SCRIPT_PATH = Path(__file__).with_name("check_hardcoded_chinese.py")
SPEC = importlib.util.spec_from_file_location("check_hardcoded_chinese", SCRIPT_PATH)
assert SPEC and SPEC.loader
hardcoded_check = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = hardcoded_check
SPEC.loader.exec_module(hardcoded_check)


class TestHardcodedChineseCheck(unittest.TestCase):
    def test_scans_production_go_python_and_frontend_sources(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            files = {
                "service/main.go": 'package service\nvar message = "请求失败"\n// 中文注释\n',
                "worker/main.py": (
                    '"""中文模块说明。"""\n'
                    'message = f"任务{task_id}失败"\n'
                    '# 中文注释\n'
                    'def run():\n'
                    '    """中文函数说明。"""\n'
                    '    return "完成"\n'
                ),
                "web/page.tsx": 'export const Page = () => <div title="标题">正文</div>\n// 中文注释\n',
                "web/index.html": '<main aria-label="首页">欢迎</main><!-- 中文注释 -->\n',
            }
            for path, content in files.items():
                target = root / path
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text(content, encoding="utf-8")

            findings = hardcoded_check.scan_repository(root, list(files))

        self.assertEqual(
            [(finding.path, finding.line) for finding in findings],
            [
                ("service/main.go", 2),
                ("web/index.html", 1),
                ("web/page.tsx", 1),
                ("worker/main.py", 2),
                ("worker/main.py", 6),
            ],
        )
        self.assertTrue(all(finding.fingerprint for finding in findings))

    def test_excludes_non_production_and_approved_business_data(self) -> None:
        excluded = (
            "service/locale/zh_cn.py",
            "service/tests/test_api.py",
            "docs/example.py",
            "service/generated/client.pb.go",
            "bkn/sample/script.py",
            "adp/bkn/bkn-backend/server/bkn-specification/examples/demo.py",
            "infra/sandbox/sandbox_control_plane/src/infrastructure/persistence/seed/default_data.py",
        )
        self.assertTrue(all(not hardcoded_check.is_production_source(path) for path in excluded))
        self.assertTrue(hardcoded_check.is_production_source("service/app.py"))
        self.assertTrue(hardcoded_check.is_production_source("web/src/App.vue"))
        self.assertTrue(hardcoded_check.is_production_source("comm-go/service.go"))

    def test_fingerprint_survives_unrelated_line_insertions(self) -> None:
        before = hardcoded_check.assign_fingerprints(
            [hardcoded_check.Finding("service/main.go", 2, 'var message = "失败"')]
        )
        after = hardcoded_check.assign_fingerprints(
            [hardcoded_check.Finding("service/main.go", 20, '    var   message = "失败"')]
        )
        self.assertEqual(before[0].fingerprint, after[0].fingerprint)

    def test_diagnostic_contains_actionable_location_and_escaped_text(self) -> None:
        finding = hardcoded_check.Finding("web/page,one.tsx", 7, 'const text = "失败 100%"')
        self.assertEqual(
            hardcoded_check.format_finding(finding),
            '::error file=web/page%2Cone.tsx,line=7::hard-coded Chinese: const text = "失败 100%25"',
        )

    def test_baseline_comparison_rejects_new_and_stale_findings(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            source = root / "service/main.go"
            source.parent.mkdir(parents=True)
            source.write_text('package service\nvar first = "失败"\n', encoding="utf-8")
            findings = hardcoded_check.scan_repository(root, ["service/main.go"])
            baseline = root / "baseline.json"
            hardcoded_check.write_baseline(baseline, findings)

            output = io.StringIO()
            with redirect_stdout(output):
                result = hardcoded_check.main(
                    ["--root", str(root), "--baseline", str(baseline)]
                )
            self.assertEqual(result, 0)
            self.assertIn("0 new", output.getvalue())

            source.write_text(
                'package service\nvar first = "失败"\nvar second = "新增"\n',
                encoding="utf-8",
            )
            output = io.StringIO()
            with redirect_stdout(output):
                result = hardcoded_check.main(
                    ["--root", str(root), "--baseline", str(baseline)]
                )
            self.assertEqual(result, 1)
            self.assertIn("file=service/main.go,line=3", output.getvalue())

            source.write_text("package service\n", encoding="utf-8")
            output = io.StringIO()
            with redirect_stdout(output):
                result = hardcoded_check.main(
                    ["--root", str(root), "--baseline", str(baseline)]
                )
            self.assertEqual(result, 1)
            self.assertIn("baseline entry is stale", output.getvalue())


if __name__ == "__main__":
    unittest.main()
