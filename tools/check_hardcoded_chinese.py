# Copyright openbkn.ai
#
# Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

"""Prevent new hard-coded Chinese text from entering production source files.

The repository still contains legacy violations.  A checked-in baseline keeps
those violations non-blocking while making additions fail CI.  Removing an
existing violation makes the baseline stale so that the same text cannot be
silently reintroduced later.
"""

from __future__ import annotations

import argparse
import ast
import fnmatch
import hashlib
import io
import json
import re
import subprocess
import sys
import tokenize
from collections import Counter
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Iterable, Sequence


REPOSITORY_ROOT = Path(__file__).resolve().parents[1]
DEFAULT_BASELINE = REPOSITORY_ROOT / "tools/hardcoded_chinese_baseline.json"
SOURCE_SUFFIXES = frozenset(
    {".go", ".py", ".js", ".jsx", ".ts", ".tsx", ".vue", ".html", ".htm", ".svelte"}
)

# These categories are intentionally explicit so reviewers can audit the scope.
# The final group contains approved business data rather than product UI/error
# copy; business data is localized through its data model, not source-code i18n.
EXCLUDED_PATHS = (
    # Locale resources.
    "**/i18n/**",
    "**/locale/**",
    "**/locales/**",
    "**/translations/**",
    # Tests and test-only assets.
    "**/test/**",
    "**/tests/**",
    "**/testdata/**",
    "**/testcases/**",
    "**/fixtures/**",
    "**/mocks/**",
    "**/*_test.go",
    "**/test_*.py",
    "**/*_test.py",
    "**/*.spec.js",
    "**/*.spec.jsx",
    "**/*.spec.ts",
    "**/*.spec.tsx",
    "**/*.test.js",
    "**/*.test.jsx",
    "**/*.test.ts",
    "**/*.test.tsx",
    # Documentation, generated code and third-party/build output.
    "docs/**",
    "help/**",
    "**/docs/**",
    "**/generated/**",
    "**/gen/**",
    "**/vendor/**",
    "**/node_modules/**",
    "**/dist/**",
    "**/build/**",
    "**/*.generated.go",
    "**/*_generated.go",
    "**/*.pb.go",
    "**/*.min.js",
    # Approved business-data/example paths.
    "bkn/**",
    "adp/bkn/bkn-backend/server/bkn-specification/examples/**",
    "infra/sandbox/sandbox_control_plane/src/infrastructure/persistence/seed/**",
)

# Include Chinese characters as well as CJK and full-width punctuation.  Product
# copy often uses a punctuation-only separator or terminator (for example
# "、" or "。"), which must be subject to the same baseline ratchet.
CJK = re.compile(r"[\u3000-\u303f\u3400-\u4dbf\u4e00-\u9fff\uf900-\ufaff\uff00-\uffef]")


@dataclass(frozen=True)
class Finding:
    path: str
    line: int
    text: str
    fingerprint: str = ""

    def with_fingerprint(self, occurrence: int) -> "Finding":
        normalized = " ".join(self.text.split())
        digest = hashlib.sha256(
            f"{self.path}\0{normalized}\0{occurrence}".encode("utf-8")
        ).hexdigest()[:20]
        return Finding(self.path, self.line, self.text, digest)


def path_matches(path: str, pattern: str) -> bool:
    """Match a repository path, including root paths for ``**/`` patterns."""
    return fnmatch.fnmatchcase(path, pattern) or (
        pattern.startswith("**/") and fnmatch.fnmatchcase(path, pattern[3:])
    )


def is_production_source(path: str) -> bool:
    normalized = PurePosixPath(path).as_posix().removeprefix("./")
    if PurePosixPath(normalized).suffix.lower() not in SOURCE_SUFFIXES:
        return False
    return not any(path_matches(normalized, pattern) for pattern in EXCLUDED_PATHS)


def tracked_files(root: Path) -> list[str]:
    """Return tracked files when possible, falling back to a filesystem walk."""
    try:
        result = subprocess.run(
            ["git", "-C", str(root), "ls-files", "-z"],
            check=True,
            capture_output=True,
        )
    except (OSError, subprocess.CalledProcessError):
        return sorted(
            path.relative_to(root).as_posix()
            for path in root.rglob("*")
            if path.is_file()
        )
    return sorted(path for path in result.stdout.decode("utf-8").split("\0") if path)


def python_findings(path: str, text: str) -> list[Finding]:
    """Find CJK in Python strings while ignoring comments, identifiers and docstrings."""
    lines = text.splitlines()
    line_numbers: set[int] = set()
    try:
        tree = ast.parse(text, filename=path)
        docstring_lines: set[int] = set()
        containers = (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)
        for node in ast.walk(tree):
            if not isinstance(node, containers) or not node.body:
                continue
            first = node.body[0]
            if not (
                isinstance(first, ast.Expr)
                and isinstance(first.value, ast.Constant)
                and isinstance(first.value.value, str)
            ):
                continue
            docstring_lines.update(range(first.lineno, first.end_lineno + 1))

        tokens = tokenize.generate_tokens(io.StringIO(text).readline)
        for token in tokens:
            token_name = tokenize.tok_name.get(token.type, "")
            if token.type != tokenize.STRING and not token_name.startswith("FSTRING_"):
                continue
            if not CJK.search(token.string):
                continue
            start_line, end_line = token.start[0], token.end[0]
            for line_number in range(start_line, end_line + 1):
                if (
                    line_number not in docstring_lines
                    and 1 <= line_number <= len(lines)
                    and CJK.search(lines[line_number - 1])
                ):
                    line_numbers.add(line_number)
    except (IndentationError, SyntaxError, tokenize.TokenError):
        # A source file should normally tokenize.  Falling back to the generic
        # scanner avoids silently skipping a malformed file before its own linter
        # gets a chance to report the syntax error.
        return c_like_findings(path, text, html_comments=False)
    return [
        Finding(path, line_number, lines[line_number - 1].strip())
        for line_number in sorted(line_numbers)
    ]


def mask_c_like_comments(text: str, *, html_comments: bool) -> str:
    """Replace C/JS and optional HTML comments while preserving line positions."""
    output = list(text)
    index = 0
    state = "code"
    quote = ""
    while index < len(text):
        current = text[index]
        following = text[index + 1] if index + 1 < len(text) else ""

        if state == "line-comment":
            if current == "\n":
                state = "code"
            else:
                output[index] = " "
            index += 1
            continue
        if state in {"block-comment", "html-comment"}:
            closing = "-->" if state == "html-comment" else "*/"
            if text.startswith(closing, index):
                for offset in range(len(closing)):
                    output[index + offset] = " "
                index += len(closing)
                state = "code"
            else:
                if current != "\n":
                    output[index] = " "
                index += 1
            continue
        if state == "string":
            if current == "\\":
                index += 2
                continue
            if current == quote:
                state = "code"
            index += 1
            continue

        if html_comments and text.startswith("<!--", index):
            for offset in range(4):
                output[index + offset] = " "
            index += 4
            state = "html-comment"
        elif current == "/" and following == "/":
            output[index] = output[index + 1] = " "
            index += 2
            state = "line-comment"
        elif current == "/" and following == "*":
            output[index] = output[index + 1] = " "
            index += 2
            state = "block-comment"
        elif current in {"'", '"', "`"}:
            quote = current
            state = "string"
            index += 1
        else:
            index += 1
    return "".join(output)


def c_like_findings(path: str, text: str, *, html_comments: bool) -> list[Finding]:
    masked = mask_c_like_comments(text, html_comments=html_comments)
    original_lines = text.splitlines()
    return [
        Finding(path, line_number, original_lines[line_number - 1].strip())
        for line_number, line in enumerate(masked.splitlines(), start=1)
        if CJK.search(line)
    ]


def scan_file(root: Path, path: str) -> list[Finding]:
    try:
        text = (root / path).read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as error:
        raise ValueError(f"{path}: cannot read UTF-8 source: {error}") from error
    suffix = PurePosixPath(path).suffix.lower()
    if suffix == ".py":
        return python_findings(path, text)
    return c_like_findings(path, text, html_comments=suffix in {".vue", ".html", ".htm", ".svelte"})


def assign_fingerprints(findings: Iterable[Finding]) -> list[Finding]:
    occurrences: Counter[tuple[str, str]] = Counter()
    result: list[Finding] = []
    for finding in sorted(findings, key=lambda item: (item.path, item.line, item.text)):
        key = (finding.path, " ".join(finding.text.split()))
        occurrence = occurrences[key]
        occurrences[key] += 1
        result.append(finding.with_fingerprint(occurrence))
    return result


def scan_repository(root: Path, paths: Sequence[str] | None = None) -> list[Finding]:
    candidates = paths if paths is not None else tracked_files(root)
    findings: list[Finding] = []
    for path in sorted(PurePosixPath(item).as_posix() for item in candidates):
        if is_production_source(path):
            findings.extend(scan_file(root, path))
    return assign_fingerprints(findings)


def load_baseline(path: Path) -> dict[str, dict[str, object]]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise ValueError(f"cannot read baseline {path}: {error}") from error
    if not isinstance(payload, dict):
        raise ValueError(f"{path}: unsupported baseline format")
    if payload.get("version") != 1 or not isinstance(payload.get("violations"), dict):
        raise ValueError(f"{path}: unsupported baseline format")
    entries: dict[str, dict[str, object]] = {}
    for source_path, fingerprints in payload["violations"].items():
        if not isinstance(source_path, str) or not isinstance(fingerprints, list):
            raise ValueError(f"{path}: violations must map source paths to fingerprint lists")
        for fingerprint in fingerprints:
            if not isinstance(fingerprint, str):
                raise ValueError(f"{path}: every baseline violation needs a fingerprint")
            if fingerprint in entries:
                raise ValueError(f"{path}: duplicate fingerprint {fingerprint}")
            entries[fingerprint] = {"path": source_path}
    if payload.get("violation_count") != len(entries):
        raise ValueError(f"{path}: violation_count does not match the fingerprint entries")
    return entries


def write_baseline(path: Path, findings: Sequence[Finding]) -> None:
    violations: dict[str, list[str]] = {}
    for finding in findings:
        violations.setdefault(finding.path, []).append(finding.fingerprint)
    payload = {
        "version": 1,
        "generated_by": "python3 tools/check_hardcoded_chinese.py --update-baseline",
        "violation_count": len(findings),
        "violations": violations,
    }
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def annotation_escape(value: object, *, property_value: bool = False) -> str:
    escaped = str(value).replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
    if property_value:
        escaped = escaped.replace(":", "%3A").replace(",", "%2C")
    return escaped


def format_finding(finding: Finding) -> str:
    snippet = finding.text if len(finding.text) <= 180 else f"{finding.text[:177]}..."
    return (
        f"::error file={annotation_escape(finding.path, property_value=True)},line={finding.line}::"
        f"hard-coded Chinese: {annotation_escape(snippet)}"
    )


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=REPOSITORY_ROOT)
    parser.add_argument("--baseline", type=Path, default=DEFAULT_BASELINE)
    parser.add_argument(
        "--update-baseline",
        action="store_true",
        help="accept the current findings as the new non-blocking baseline",
    )
    parser.add_argument(
        "--report-only",
        action="store_true",
        help="report current findings without comparing or failing",
    )
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    root = args.root.resolve()
    baseline = args.baseline
    if not baseline.is_absolute():
        baseline = root / baseline
    try:
        findings = scan_repository(root)
        if args.update_baseline:
            write_baseline(baseline, findings)
            print(f"Updated {baseline} with {len(findings)} existing violation(s).")
            return 0
        if args.report_only:
            for finding in findings:
                print(format_finding(finding))
            print(f"Found {len(findings)} hard-coded Chinese violation(s); report-only mode does not fail.")
            return 0

        baseline_entries = load_baseline(baseline)
    except ValueError as error:
        print(f"error: {error}", file=sys.stderr)
        return 2

    current = {finding.fingerprint: finding for finding in findings}
    new_fingerprints = sorted(set(current) - set(baseline_entries))
    stale_fingerprints = sorted(set(baseline_entries) - set(current))

    for fingerprint in new_fingerprints:
        print(format_finding(current[fingerprint]))
    for fingerprint in stale_fingerprints:
        entry = baseline_entries[fingerprint]
        print(
            "::warning file={path},line=1::baseline entry is stale; "
            "run the baseline update command to ratchet it down".format(
                path=annotation_escape(
                    entry.get("path", "tools/hardcoded_chinese_baseline.json"),
                    property_value=True,
                ),
            )
        )

    if new_fingerprints or stale_fingerprints:
        print(
            f"Hard-coded Chinese check failed: {len(new_fingerprints)} new, "
            f"{len(stale_fingerprints)} stale baseline entry/entries."
        )
        return 1
    print(f"Hard-coded Chinese check passed ({len(findings)} baseline violation(s), 0 new).")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
